package pkg

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"

	"github.com/klauspost/compress/zstd"
)

var (
	Magic          = [26]byte{'K', 'e', 'e', 'p', 'C', 'a', 'l', 'm', 'A', 'n', 'd', 'F', 'u', 'c', 'k', 'T', 'h', 'e', 'R', 'u', 's', 's', 'i', 'a', 'n', 's'}
	Version uint16 = 2
)

const (
	FlagCompressed = 1 << 0
)

// ---------- FILE STRUCTURES ----------

type FileHeader struct {
	Magic     [26]byte
	Version   uint16
	Flags     uint16
	IndexSize uint64
}

type IndexEntry struct {
	PathOffset      uint32
	PathLength      uint16
	Flags           uint16
	DataOffset      uint64
	CompressionSize uint64
	RawSize         uint64
}

type PackOptions struct {
	Compress      bool
	IncludeParent bool
	// Level: "fast", "default", "best"
	Level   string
	Workers int
	// Method: "zstd" (default), "lz77", "huffman"
	Method string
}

// ---------- STRING TABLE ----------

type StringTable struct {
	strings map[string]uint32
	buf     bytes.Buffer
}

func NewStringTable() *StringTable {
	return &StringTable{strings: make(map[string]uint32)}
}

func (st *StringTable) Add(s string) uint32 {
	if off, ok := st.strings[s]; ok {
		return off
	}
	off := uint32(st.buf.Len())
	st.buf.WriteString(s)
	st.strings[s] = off
	return off
}

func (st *StringTable) Bytes() []byte { return st.buf.Bytes() }

// ---------- COMPRESSION ----------

const (
	entrySize = 4 + 2 + 2 + 8 + 8 + 8 // PathOffset + PathLength + Flags + DataOffset + CompressionSize + RawSize
)

const (
	FlagSolid = 1 << 1
)

var skipCompressExt = map[string]bool{
	".zip": true, ".gz": true, ".mp3": true, ".mp4": true,
	".png": true, ".jpg": true, ".jpeg": true,
}

func shouldCompress(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return !skipCompressExt[ext]
}

var decPool sync.Pool

func init() {
	decPool = sync.Pool{New: func() any {
		dec, _ := zstd.NewReader(nil)
		return dec
	}}
}

func decompressBytes(src []byte) ([]byte, error) {
	d := decPool.Get().(*zstd.Decoder)
	out, err := d.DecodeAll(src, nil)
	decPool.Put(d)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// compressWithMethod compresses data using the specified method
func compressWithMethod(src io.Reader, method string, level zstd.EncoderLevel) (io.Reader, error) {
	method = strings.ToLower(method)
	if method == "" {
		method = "zstd" // default method
	}

	switch method {
	case "lz77":
		return CompressLZ77(src)
	case "huffman":
		return CompressHuffman(src)
	default:
		// zstd is the default
		data, err := io.ReadAll(src)
		if err != nil {
			return nil, err
		}

		var out bytes.Buffer
		enc, err := zstd.NewWriter(&out, zstd.WithEncoderLevel(level))
		if err != nil {
			return nil, err
		}
		if _, err := enc.Write(data); err != nil {
			enc.Close()
			return nil, err
		}
		enc.Close()
		return bytes.NewReader(out.Bytes()), nil
	}
}

// ---------- PACK ----------

func Pack(sources []string, outPath string, opts PackOptions) error {
	// expand sources into absolute file list with rel paths
	type srcSpec struct {
		src     string
		baseDir string
	}
	var specs []srcSpec
	for _, s := range sources {
		s = filepath.Clean(s)
		fi, err := os.Stat(s)
		if err != nil {
			return err
		}
		if fi.IsDir() {
			if opts.IncludeParent {
				specs = append(specs, srcSpec{src: s, baseDir: filepath.Dir(s)})
			} else {
				specs = append(specs, srcSpec{src: s, baseDir: s})
			}
		} else {
			// single file: baseDir is parent dir unless includeParent true
			if opts.IncludeParent {
				specs = append(specs, srcSpec{src: s, baseDir: filepath.Dir(s)})
			} else {
				specs = append(specs, srcSpec{src: s, baseDir: filepath.Dir(s)})
			}
		}
	}

	var paths []struct {
		full string
		base string
	}
	for _, sp := range specs {
		fi, err := os.Stat(sp.src)
		if err != nil {
			return err
		}
		if fi.IsDir() {
			err := filepath.Walk(sp.src, func(p string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}
				// Skip directories and symlinks
				if info.IsDir() {
					return nil
				}
				// Check if it's a symlink and skip if it is
				if (info.Mode() & os.ModeSymlink) != 0 {
					return nil
				}
				paths = append(paths, struct{ full, base string }{full: p, base: sp.baseDir})
				return nil
			})
			if err != nil {
				return err
			}
		} else {
			paths = append(paths, struct{ full, base string }{full: sp.src, base: sp.baseDir})
		}
	}

	staged := []struct {
		entry     IndexEntry
		tmpPath   string
		tmpOffset int64
	}{}

	st := NewStringTable()

	// Read files first and build string table; we'll stream-compress to temp files
	type fileItem struct {
		path   string
		rel    string
		offset uint32
	}

	files := make([]fileItem, 0, len(paths))
	for _, p := range paths {
		rel, _ := filepath.Rel(p.base, p.full)
		rel = filepath.ToSlash(rel)
		pathOffset := st.Add(rel)
		files = append(files, fileItem{path: p.full, rel: rel, offset: pathOffset})
	}

	// Note: Solid compression disabled due to temp file handling issues
	// Method-specific compression works well for all methods
	// compute total/raw size - currently unused with solid compression disabled
	var totalRaw int64
	for _, fi := range files {
		if info, err := os.Stat(fi.path); err == nil {
			totalRaw += info.Size()
		}
	}
	_ = totalRaw // unused after disabling solid compression

	// create encoder pool based on opts.Level
	// default to better compression to improve overall archive ratio
	level := zstd.SpeedBetterCompression
	switch strings.ToLower(opts.Level) {
	case "fast":
		level = zstd.SpeedFastest
	case "best":
		level = zstd.SpeedBestCompression
	default:
		level = zstd.SpeedBetterCompression
	}

	workers := opts.Workers
	if workers <= 0 {
		workers = 0
	}
	if workers <= 0 {
		// default to number of logical CPUs
		workers = 1
		if n := runtime.NumCPU(); n > 0 {
			workers = n
		}
	}

	type result struct {
		idx        int
		tmpPath    string
		compSize   int64
		rawSize    int64
		compressed bool
		tmpOffset  int64
		err        error
	}

	jobs := make(chan int, len(files))
	results := make(chan result, len(files))

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				fi := files[idx]
				srcF, err := os.Open(fi.path)
				if err != nil {
					results <- result{idx: idx, err: err}
					continue
				}
				srcStat, _ := srcF.Stat()
				tmp, err := os.CreateTemp("", "sqar-enc-*")
				if err != nil {
					srcF.Close()
					results <- result{idx: idx, err: err}
					continue
				}
				var compSize int64
				compressed := false
				if opts.Compress && shouldCompress(fi.path) {
					// Use selected compression method
					compReader, err := compressWithMethod(srcF, opts.Method, level)
					if err != nil {
						srcF.Close()
						tmp.Close()
						results <- result{idx: idx, err: err}
						continue
					}
					if _, err := io.Copy(tmp, compReader); err != nil {
						srcF.Close()
						tmp.Close()
						results <- result{idx: idx, err: err}
						continue
					}
					compressed = true
				} else {
					if _, err := io.Copy(tmp, srcF); err != nil {
						srcF.Close()
						tmp.Close()
						results <- result{idx: idx, err: err}
						continue
					}
				}
				srcF.Close()
				tmpInfo, _ := tmp.Stat()
				compSize = tmpInfo.Size()
				tmp.Close()
				results <- result{idx: idx, tmpPath: tmp.Name(), compSize: compSize, rawSize: srcStat.Size(), compressed: compressed, err: nil}
			}
		}()
	}

	for i := range files {
		jobs <- i
	}
	close(jobs)

	go func() {
		wg.Wait()
		close(results)
	}()

	// Note: Solid compression disabled due to temp file handling issues
	// Method-specific compression works well for all methods
	_ = totalRaw // avg calculation removed with solid compression

	solid := false // Keep solid mode disabled for now

	if solid {
		tmpAll, err := os.CreateTemp("", "sqar-solid-*")
		if err != nil {
			return err
		}
		enc, err := zstd.NewWriter(tmpAll, zstd.WithEncoderLevel(level))
		if err != nil {
			tmpAll.Close()
			return err
		}

		// iterate files sequentially, writing rawSize+data into encoder, recording offsets/sizes
		var curOffsetInTmp int64
		for i, fi := range files {
			srcF, err := os.Open(fi.path)
			if err != nil {
				enc.Close()
				tmpAll.Close()
				return err
			}
			srcInfo, _ := srcF.Stat()
			rawSize := uint64(srcInfo.Size())

			// current compressed file size before writing
			stInfo, _ := tmpAll.Stat()
			start := stInfo.Size()

			// write rawSize then file data into encoder
			if err := binary.Write(enc, binary.LittleEndian, rawSize); err != nil {
				srcF.Close()
				enc.Close()
				tmpAll.Close()
				return err
			}
			if _, err := io.Copy(enc, srcF); err != nil {
				srcF.Close()
				enc.Close()
				tmpAll.Close()
				return err
			}
			srcF.Close()
			// flush encoder to ensure data reaches underlying file
			if err := enc.Flush(); err != nil {
				enc.Close()
				tmpAll.Close()
				return err
			}

			// new size and compSize
			stInfo2, _ := tmpAll.Stat()
			end := stInfo2.Size()
			compSize := end - start

			entry := IndexEntry{PathOffset: fi.offset, PathLength: uint16(len(fi.rel)), Flags: 1, DataOffset: 0, CompressionSize: uint64(compSize), RawSize: rawSize}
			staged = append(staged, struct {
				entry     IndexEntry
				tmpPath   string
				tmpOffset int64
			}{entry: entry, tmpPath: tmpAll.Name(), tmpOffset: start})
			curOffsetInTmp += compSize + 8
			_ = i
		}

		enc.Close()
		tmpAll.Close()
	} else {
		stagedMap := make([]struct {
			entry     IndexEntry
			tmpPath   string
			tmpOffset int64
		}, len(files))
		for r := range results {
			if r.err != nil {
				return r.err
			}
			fi := files[r.idx]
			entry := IndexEntry{PathOffset: fi.offset, PathLength: uint16(len(fi.rel)), Flags: 0}
			if r.compressed {
				entry.Flags = 1
			}
			entry.CompressionSize = uint64(r.compSize)
			entry.RawSize = uint64(r.rawSize)
			stagedMap[r.idx] = struct {
				entry     IndexEntry
				tmpPath   string
				tmpOffset int64
			}{entry: entry, tmpPath: r.tmpPath, tmpOffset: 0}
		}

		// preserve original ordering
		for i := range stagedMap {
			staged = append(staged, stagedMap[i])
		}
	}

	// include 4 bytes for entry count
	indexSize := uint64(len(st.Bytes())) + uint64(len(staged))*uint64(entrySize) + 4

	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()

	header := FileHeader{
		Magic:     Magic,
		Version:   Version,
		Flags:     0,
		IndexSize: indexSize,
	}
	if opts.Compress {
		header.Flags |= FlagCompressed
	}
	// if solid compression chosen, mark flag
	if solid {
		header.Flags |= FlagSolid
	}

	if err := binary.Write(f, binary.LittleEndian, &header); err != nil {
		return err
	}

	// Write number of entries, then entries (fixed-size), then string table, then file data.
	if err := binary.Write(f, binary.LittleEndian, uint32(len(staged))); err != nil {
		return err
	}
	curOffset := uint64(0)
	for i := range staged {
		staged[i].entry.DataOffset = curOffset
		e := &staged[i].entry
		if err := binary.Write(f, binary.LittleEndian, e.PathOffset); err != nil {
			return err
		}
		if err := binary.Write(f, binary.LittleEndian, e.PathLength); err != nil {
			return err
		}
		if err := binary.Write(f, binary.LittleEndian, e.Flags); err != nil {
			return err
		}
		if err := binary.Write(f, binary.LittleEndian, e.DataOffset); err != nil {
			return err
		}
		if err := binary.Write(f, binary.LittleEndian, e.CompressionSize); err != nil {
			return err
		}
		if err := binary.Write(f, binary.LittleEndian, e.RawSize); err != nil {
			return err
		}

		if header.Flags&FlagSolid != 0 {
			curOffset += e.CompressionSize
		} else {
			curOffset += e.CompressionSize + 8
		}
	}

	// write string table
	if _, err := f.Write(st.Bytes()); err != nil {
		return err
	}

	// write data (current file offset is start of data)
	if header.Flags&FlagSolid != 0 {
		// solid: staged entries point into one or more compressed blobs; copy per-entry ranges
		for _, s := range staged {
			// open blob and copy the exact compressed range
			tmp, err := os.Open(s.tmpPath)
			if err != nil {
				return err
			}
			if _, err := tmp.Seek(s.tmpOffset, io.SeekStart); err != nil {
				tmp.Close()
				return err
			}
			if _, err := io.CopyN(f, tmp, int64(s.entry.CompressionSize)); err != nil {
				tmp.Close()
				return err
			}
			tmp.Close()
			os.Remove(s.tmpPath)
		}
	} else {
		for _, s := range staged {
			if err := binary.Write(f, binary.LittleEndian, s.entry.RawSize); err != nil {
				return err
			}
			// stream copy from temp file
			tmp, err := os.Open(s.tmpPath)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tmp); err != nil {
				tmp.Close()
				return err
			}
			tmp.Close()
			os.Remove(s.tmpPath)
		}
	}

	return nil
}

// ---------- LIST ----------

func List(archive string) ([]IndexEntry, []byte, error) {
	f, err := os.Open(archive)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()

	var h FileHeader
	if err := binary.Read(f, binary.LittleEndian, &h); err != nil {
		return nil, nil, err
	}
	if h.Magic != Magic {
		return nil, nil, errors.New("not a SQAR archive")
	}

	stLen := int(h.IndexSize)
	stBytes := make([]byte, stLen)
	if _, err := io.ReadFull(f, stBytes); err != nil {
		return nil, nil, err
	}

	if stLen < 4 {
		return nil, nil, errors.New("index too small")
	}
	read := 0
	count := int(binary.LittleEndian.Uint32(stBytes[read : read+4]))
	read += 4

	entries := make([]IndexEntry, 0, count)
	for i := 0; i < count; i++ {
		if read+entrySize > stLen {
			return nil, nil, errors.New("index truncated")
		}
		e := IndexEntry{}
		e.PathOffset = binary.LittleEndian.Uint32(stBytes[read : read+4])
		read += 4
		e.PathLength = binary.LittleEndian.Uint16(stBytes[read : read+2])
		read += 2
		e.Flags = binary.LittleEndian.Uint16(stBytes[read : read+2])
		read += 2
		e.DataOffset = binary.LittleEndian.Uint64(stBytes[read : read+8])
		read += 8
		e.CompressionSize = binary.LittleEndian.Uint64(stBytes[read : read+8])
		read += 8
		e.RawSize = binary.LittleEndian.Uint64(stBytes[read : read+8])
		read += 8
		entries = append(entries, e)
	}

	// remaining bytes are the string table
	stringTable := stBytes[read:]

	return entries, stringTable, nil
}

// ---------- UNPACK (memory-mapped) ----------

func Unpack(archive, outDir string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return err
	}

	data, err := syscall.Mmap(int(f.Fd()), 0, int(stat.Size()), syscall.PROT_READ, syscall.MAP_SHARED)
	if err != nil {
		return err
	}
	defer syscall.Munmap(data)

	var h FileHeader
	buf := bytes.NewReader(data[:binary.Size(FileHeader{})])
	binary.Read(buf, binary.LittleEndian, &h)

	entries, stBytes, err := List(archive)
	if err != nil {
		return err
	}

	// data region starts after header + index region
	dataStart := int64(binary.Size(FileHeader{})) + int64(h.IndexSize)
	for _, e := range entries {
		// unpack each entry
		offset := dataStart + int64(e.DataOffset)
		var out []byte
		if h.Flags&FlagSolid != 0 {
			// solid mode: entry.DataOffset points to start of compressed segment
			payloadStart := offset
			payloadEnd := payloadStart + int64(e.CompressionSize)
			if payloadEnd > int64(len(data)) {
				return errors.New("archive corrupted: invalid payload size")
			}
			payload := data[payloadStart:payloadEnd]
			if e.Flags&1 != 0 {
				dec, err := decompressBytes(payload)
				if err != nil {
					return err
				}
				if len(dec) < 8 {
					return errors.New("archive corrupted: invalid decompressed segment")
				}
				// first 8 bytes are rawSize, remainder is file data
				rawSize := binary.LittleEndian.Uint64(dec[:8])
				_ = rawSize
				out = dec[8:]
			} else {
				out = make([]byte, len(payload))
				copy(out, payload)
			}
		} else {
			if offset+8 > int64(len(data)) {
				return errors.New("archive corrupted: invalid data offset")
			}
			payloadStart := offset + 8
			payloadEnd := payloadStart + int64(e.CompressionSize)
			if payloadEnd > int64(len(data)) {
				return errors.New("archive corrupted: invalid payload size")
			}
			payload := data[payloadStart:payloadEnd]
			if e.Flags&1 != 0 {
				dec, err := decompressBytes(payload)
				if err != nil {
					return err
				}
				out = dec
			} else {
				out = make([]byte, len(payload))
				copy(out, payload)
			}
		}

		dest := filepath.Join(outDir, string(stBytes[e.PathOffset:e.PathOffset+uint32(e.PathLength)]))
		os.MkdirAll(filepath.Dir(dest), 0755)
		if err := os.WriteFile(dest, out, 0644); err != nil {
			return err
		}
	}

	return nil
}

// ---------- APPEND ----------

// Append adds new files/directories to an existing archive by rewriting it.
func Append(archive string, sources []string, opts PackOptions) error {
	// open existing archive and read header
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()

	var h FileHeader
	if err := binary.Read(f, binary.LittleEndian, &h); err != nil {
		return err
	}

	entries, stBytes, err := List(archive)
	if err != nil {
		return err
	}

	// copy existing payloads to temp files
	dataStart := int64(binary.Size(FileHeader{})) + int64(h.IndexSize)
	existing := make([]struct {
		path    string
		entry   IndexEntry
		tmpPath string
	}, 0, len(entries))

	for _, e := range entries {
		payloadOffset := dataStart + int64(e.DataOffset)
		// payload starts after 8-byte raw size header unless archive was solid
		payloadStart := payloadOffset + 8
		if h.Flags&FlagSolid != 0 {
			payloadStart = payloadOffset
		}
		tmp, err := os.CreateTemp("", "sqar-old-*")
		if err != nil {
			return err
		}
		// copy compressed payload
		if _, err := f.Seek(payloadStart, io.SeekStart); err != nil {
			tmp.Close()
			return err
		}
		if _, err := io.CopyN(tmp, f, int64(e.CompressionSize)); err != nil {
			tmp.Close()
			return err
		}
		tmp.Close()
		// extract path string from old string table
		path := string(stBytes[e.PathOffset : e.PathOffset+uint32(e.PathLength)])
		existing = append(existing, struct {
			path    string
			entry   IndexEntry
			tmpPath string
		}{path: path, entry: e, tmpPath: tmp.Name()})
	}

	// now pack new sources to temp files (reuse Pack logic but without writing final archive)
	// gather new files
	type srcSpec struct {
		src     string
		baseDir string
	}
	var specs []srcSpec
	for _, s := range sources {
		s = filepath.Clean(s)
		fi, err := os.Stat(s)
		if err != nil {
			return err
		}
		if fi.IsDir() {
			if opts.IncludeParent {
				specs = append(specs, srcSpec{src: s, baseDir: filepath.Dir(s)})
			} else {
				specs = append(specs, srcSpec{src: s, baseDir: s})
			}
		} else {
			if opts.IncludeParent {
				specs = append(specs, srcSpec{src: s, baseDir: filepath.Dir(s)})
			} else {
				specs = append(specs, srcSpec{src: s, baseDir: filepath.Dir(s)})
			}
		}
	}
	var newPaths []struct{ full, base string }
	for _, sp := range specs {
		fi, err := os.Stat(sp.src)
		if err != nil {
			return err
		}
		if fi.IsDir() {
			err := filepath.Walk(sp.src, func(p string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}
				if !info.IsDir() {
					newPaths = append(newPaths, struct{ full, base string }{full: p, base: sp.baseDir})
				}
				return nil
			})
			if err != nil {
				return err
			}
		} else {
			newPaths = append(newPaths, struct{ full, base string }{full: sp.src, base: sp.baseDir})
		}
	}

	// build new string table starting from empty and adding existing paths first
	stNew := NewStringTable()
	type stagedEntry struct {
		entry   IndexEntry
		tmpPath string
	}
	staged := make([]stagedEntry, 0, len(existing)+len(newPaths))
	// existing
	for _, ex := range existing {
		off := stNew.Add(ex.path)
		e := ex.entry
		e.PathOffset = off
		staged = append(staged, stagedEntry{entry: e, tmpPath: ex.tmpPath})
	}

	// compress new files to temp files
	for _, np := range newPaths {
		rel, _ := filepath.Rel(np.base, np.full)
		rel = filepath.ToSlash(rel)
		off := stNew.Add(rel)
		srcF, err := os.Open(np.full)
		if err != nil {
			return err
		}
		srcStat, _ := srcF.Stat()
		tmp, err := os.CreateTemp("", "sqar-new-*")
		if err != nil {
			srcF.Close()
			return err
		}
		compressed := false
		if opts.Compress && shouldCompress(np.full) {
			enc, err := zstd.NewWriter(tmp, zstd.WithEncoderLevel(zstd.SpeedDefault))
			if err != nil {
				srcF.Close()
				tmp.Close()
				return err
			}
			if _, err := io.Copy(enc, srcF); err != nil {
				enc.Close()
				srcF.Close()
				tmp.Close()
				return err
			}
			enc.Close()
			compressed = true
		} else {
			if _, err := io.Copy(tmp, srcF); err != nil {
				srcF.Close()
				tmp.Close()
				return err
			}
		}
		srcF.Close()
		tmp.Close()
		info, _ := os.Stat(tmp.Name())
		e := IndexEntry{PathOffset: off, PathLength: uint16(len(rel)), Flags: 0, CompressionSize: uint64(info.Size()), RawSize: uint64(srcStat.Size())}
		if compressed {
			e.Flags = 1
		}
		staged = append(staged, stagedEntry{entry: e, tmpPath: tmp.Name()})
	}

	// write new archive to temp file then replace original
	outTmp := archive + ".sqar.tmp"
	of, err := os.Create(outTmp)
	if err != nil {
		return err
	}
	defer of.Close()

	indexSize := uint64(len(stNew.Bytes())) + uint64(len(staged))*uint64(entrySize) + 4
	headerNew := FileHeader{Magic: Magic, Version: Version, Flags: 0, IndexSize: indexSize}
	if opts.Compress {
		headerNew.Flags |= FlagCompressed
	}
	if err := binary.Write(of, binary.LittleEndian, &headerNew); err != nil {
		return err
	}
	if err := binary.Write(of, binary.LittleEndian, uint32(len(staged))); err != nil {
		return err
	}

	// write entries
	curOffset := uint64(0)
	for i := range staged {
		staged[i].entry.DataOffset = curOffset
		e := &staged[i].entry
		if err := binary.Write(of, binary.LittleEndian, e.PathOffset); err != nil {
			return err
		}
		if err := binary.Write(of, binary.LittleEndian, e.PathLength); err != nil {
			return err
		}
		if err := binary.Write(of, binary.LittleEndian, e.Flags); err != nil {
			return err
		}
		if err := binary.Write(of, binary.LittleEndian, e.DataOffset); err != nil {
			return err
		}
		if err := binary.Write(of, binary.LittleEndian, e.CompressionSize); err != nil {
			return err
		}
		if err := binary.Write(of, binary.LittleEndian, e.RawSize); err != nil {
			return err
		}
		curOffset += e.CompressionSize + 8
	}

	// write string table
	if _, err := of.Write(stNew.Bytes()); err != nil {
		return err
	}

	// write data
	for _, s := range staged {
		if err := binary.Write(of, binary.LittleEndian, s.entry.RawSize); err != nil {
			return err
		}
		tmp, err := os.Open(s.tmpPath)
		if err != nil {
			return err
		}
		if _, err := io.Copy(of, tmp); err != nil {
			tmp.Close()
			return err
		}
		tmp.Close()
		os.Remove(s.tmpPath)
	}

	of.Close()
	f.Close()

	// replace original archive file
	if err := os.Rename(outTmp, archive); err != nil {
		return err
	}

	return nil
}
