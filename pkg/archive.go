package pkg

import (
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
)

var (
	Magic          = [26]byte{'K', 'e', 'e', 'p', 'C', 'a', 'l', 'm', 'A', 'n', 'd', 'F', 'u', 'c', 'k', 'T', 'h', 'e', 'R', 'u', 's', 's', 'i', 'a', 'n', 's'}
	Version uint16 = 1
)

const (
	FlagCompressed = 1 << 0
)

type FileHeader struct {
	Magic     [26]byte
	Version   uint16
	Flags     uint16
	IndexSize uint64
}

type IndexEntry struct {
	PathLength uint16
	Path       string

	DataOffset      uint64
	CompressionSize uint64
	RawSize         uint64
}

type PackOptions struct {
	Compress      bool
	IncludeParent bool
}

// SQAR Compression system

const (
	minMatch = 4
	window   = 65535
)

func sqarCompress(src []byte) []byte {
	out := make([]byte, 0, len(src))
	i := 0

	for i < len(src) {
		bestLen := 0
		bestOff := 0

		start := i - window
		if start < 0 {
			start = 0
		}

		for j := i - 1; j >= start; j-- {
			l := 0
			for i+l < len(src) && src[j+1] == src[i+l] {
				l++
				if l >= 18 {
					break
				}
			}
			if l >= minMatch && l > bestLen {
				bestLen = l
				bestOff = i - j
			}
		}

		if bestLen >= minMatch {
			token := byte(bestLen - minMatch)
			out = append(out, token)
			out = append(out, byte(bestOff), byte(bestOff>>8))
			i += bestLen
		} else {
			out = append(out, 0xF0|1)
			out = append(out, src[i])
			i++
		}
	}

	return out
}

func sqarDecompress(src []byte, rawSize uint64) []byte {
	out := make([]byte, 0, rawSize)
	i := 0

	for i < len(src) {
		token := src[i]
		i++

		litLen := int(token >> 4)
		matchLen := int(token&0x0F) + minMatch

		if litLen > 0 {
			out = append(out, src[i:i+litLen]...)
			i += litLen
			continue
		}

		off := int(src[i]) | int(src[i+1])<<8
		i += 2

		start := len(out) - off
		for j := 0; j < matchLen; j++ {
			out = append(out, out[start+j])
		}
	}

	return out
}

func Pack(src, out string, opts PackOptions) error {
	src = filepath.Clean(src)
	baseDirectory := src
	if !opts.IncludeParent {
		baseDirectory = filepath.Dir(src)
	}

	var paths []string

	// Scan recursively through the source directory
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

	type stagedFile struct {
		entry IndexEntry
		data  []byte
	}

	var staged []stagedFile

	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}

		payload := raw
		if opts.Compress {
			payload = sqarCompress(raw)
		}

		rel, _ := filepath.Rel(baseDirectory, p)
		rel = filepath.ToSlash(rel)

		staged = append(staged, stagedFile{
			entry: IndexEntry{
				Path:            rel,
				PathLength:      uint16(len(rel)),
				CompressionSize: uint64(len(payload)),
				RawSize:         uint64(len(raw)),
			},
			data: payload,
		})
	}

	indexSize := uint64(0)
	for _, s := range staged {
		indexSize += 2 + uint64(len(s.entry.Path)) + 8 + 8 + 8
	}

	f, err := os.Create(out + ".sqar")
	if err != nil {
		return err
	}
	defer f.Close()

	header := FileHeader{
		Magic:     Magic,
		Version:   Version,
		IndexSize: indexSize,
	}
	if opts.Compress {
		header.Flags |= FlagCompressed
	}

	binary.Write(f, binary.LittleEndian, &header)

	dataStart := int64(int64(binary.Size(header)) + int64(indexSize))
	curOffset := uint64(0)

	for i := range staged {
		staged[i].entry.DataOffset = curOffset

		binary.Write(f, binary.LittleEndian, staged[i].entry.PathLength)
		f.WriteString(staged[i].entry.Path)
		binary.Write(f, binary.LittleEndian, staged[i].entry.DataOffset)
		binary.Write(f, binary.LittleEndian, staged[i].entry.CompressionSize)
		binary.Write(f, binary.LittleEndian, staged[i].entry.RawSize)

		curOffset += staged[i].entry.CompressionSize + 8
	}

	f.Seek(dataStart, io.SeekStart)

	for _, s := range staged {
		binary.Write(f, binary.LittleEndian, s.entry.RawSize)
		f.Write(s.data)
	}

	return nil
}

func List(archive string) ([]IndexEntry, error) {
	f, err := os.Open(archive)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var h FileHeader
	if err := binary.Read(f, binary.LittleEndian, &h); err != nil {
		return nil, err
	}
	if h.Magic != Magic {
		return nil, errors.New("file is not a valid SQAR archive")
	}

	var entries []IndexEntry
	read := uint64(0)

	for read < h.IndexSize {
		var e IndexEntry
		binary.Read(f, binary.LittleEndian, &e.PathLength)
		buf := make([]byte, e.PathLength)
		f.Read(buf)
		e.Path = string(buf)

		binary.Read(f, binary.LittleEndian, &e.DataOffset)
		binary.Read(f, binary.LittleEndian, &e.CompressionSize)
		binary.Read(f, binary.LittleEndian, &e.RawSize)

		read += 2 + uint64(e.PathLength) + 8 + 8 + 8
		entries = append(entries, e)
	}

	return entries, nil
}

func Unpack(archive, outDir string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()

	var h FileHeader
	binary.Read(f, binary.LittleEndian, &h)

	entries, err := List(archive)
	if err != nil {
		return err
	}

	database := int64(int64(binary.Size(h)) + int64(h.IndexSize))

	for _, e := range entries {
		f.Seek(database+int64(e.DataOffset), io.SeekStart)

		var rawSize uint64
		binary.Read(f, binary.LittleEndian, &rawSize)

		buf := make([]byte, e.CompressionSize)
		f.Read(buf)

		var out []byte
		if h.Flags&FlagCompressed != 0 {
			out = sqarDecompress(buf, rawSize)
		} else {
			out = buf
		}

		dest := filepath.Join(outDir, e.Path)
		os.MkdirAll(filepath.Dir(dest), 0755)
		os.WriteFile(dest, out, 0644)
	}

	return nil
}
