package pkg

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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

var skipCompressExt = map[string]bool{
	".zip": true, ".gz": true, ".mp3": true, ".mp4": true,
	".png": true, ".jpg": true, ".jpeg": true,
}

func shouldCompress(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return !skipCompressExt[ext]
}

var (
	zstdEnc *zstd.Encoder
	zstdDec *zstd.Decoder
)

func init() {
	enc, _ := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))
	dec, _ := zstd.NewReader(nil)
	zstdEnc = enc
	zstdDec = dec
}

func compressBytes(src []byte) ([]byte, error) {
	out := zstdEnc.EncodeAll(src, nil)
	return out, nil
}

func decompressBytes(src []byte) ([]byte, error) {
	out, err := zstdDec.DecodeAll(src, nil)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ---------- PACK ----------

func Pack(src, outPath string, opts PackOptions) error {
	src = filepath.Clean(src)
	baseDir := src
	if !opts.IncludeParent {
		baseDir = filepath.Dir(src)
	}

	var paths []string
	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		return err
	}

	staged := []struct {
		entry IndexEntry
		data  []byte
	}{}

	st := NewStringTable()

	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}

		rel, _ := filepath.Rel(baseDir, p)
		rel = filepath.ToSlash(rel)
		pathOffset := st.Add(rel)

		entry := IndexEntry{
			PathOffset: pathOffset,
			PathLength: uint16(len(rel)),
			Flags:      0,
		}

		payload := raw
		if opts.Compress && shouldCompress(p) {
			c, err := compressBytes(raw)
			if err != nil {
				return err
			}
			payload = c
			entry.Flags = 1
		}

		entry.CompressionSize = uint64(len(payload))
		entry.RawSize = uint64(len(raw))

		staged = append(staged, struct {
			entry IndexEntry
			data  []byte
		}{entry: entry, data: payload})
	}

	// include 4 bytes for entry count
	indexSize := uint64(len(st.Bytes())) + uint64(len(staged))*uint64(entrySize) + 4

	f, err := os.Create(outPath + ".sqar")
	if err != nil {
		return err
	}
	defer f.Close()

	// debug: compute index size
	// write header
	fmt.Printf("DEBUG PACK: staged=%d stringTableLen=%d entrySize=%d indexSize=%d\n", len(staged), len(st.Bytes()), entrySize, indexSize)

	header := FileHeader{
		Magic:     Magic,
		Version:   Version,
		Flags:     0,
		IndexSize: indexSize,
	}
	if opts.Compress {
		header.Flags |= FlagCompressed
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

		curOffset += e.CompressionSize + 8
	}

	// write string table
	if _, err := f.Write(st.Bytes()); err != nil {
		return err
	}

	// write data (current file offset is start of data)
	for _, s := range staged {
		if err := binary.Write(f, binary.LittleEndian, s.entry.RawSize); err != nil {
			return err
		}
		if _, err := f.Write(s.data); err != nil {
			return err
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

	fmt.Printf("DEBUG: entries=%d stringTableLen=%d\n", len(entries), len(stBytes))

	// data region starts after header + index region
	dataStart := int64(binary.Size(FileHeader{})) + int64(h.IndexSize)
	for _, e := range entries {
		fmt.Printf("DEBUG: entry pathOffset=%d pathLen=%d flags=%d dataOffset=%d compSize=%d rawSize=%d\n", e.PathOffset, e.PathLength, e.Flags, e.DataOffset, e.CompressionSize, e.RawSize)
		offset := dataStart + int64(e.DataOffset)
		if offset+8 > int64(len(data)) {
			return errors.New("archive corrupted: invalid data offset")
		}
		payloadStart := offset + 8
		payloadEnd := payloadStart + int64(e.CompressionSize)
		if payloadEnd > int64(len(data)) {
			return errors.New("archive corrupted: invalid payload size")
		}
		payload := data[payloadStart:payloadEnd]

		var out []byte
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

		dest := filepath.Join(outDir, string(stBytes[e.PathOffset:e.PathOffset+uint32(e.PathLength)]))
		os.MkdirAll(filepath.Dir(dest), 0755)
		if err := os.WriteFile(dest, out, 0644); err != nil {
			return err
		}
	}

	return nil
}
