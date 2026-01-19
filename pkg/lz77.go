package pkg

import (
	"bytes"
	"encoding/binary"
	"io"
)

// LZ77 compression - sliding window compression algorithm
// Uses a 32KB window for matching patterns

const (
	LZ77WindowSize = 1 << 15       // 32KB sliding window
	LZ77MinMatch   = 3             // Minimum match length
	LZ77MaxMatch   = (1 << 16) - 1 // Maximum match length (64KB)
)

type LZ77Token struct {
	IsLiteral bool
	Literal   byte
	Distance  uint16
	Length    uint16
}

type LZ77Compressor struct {
	window []byte
	pos    int
}

// CompressLZ77 compresses data using LZ77 algorithm
func CompressLZ77(src io.Reader) (io.Reader, error) {
	data, err := io.ReadAll(src)
	if err != nil {
		return nil, err
	}

	var out bytes.Buffer
	binary.Write(&out, binary.LittleEndian, uint32(len(data)))

	comp := &LZ77Compressor{window: make([]byte, 0, LZ77WindowSize)}

	i := 0
	for i < len(data) {
		// Try to find a match in the window
		distance, length := comp.findBestMatch(data, i)

		if length >= LZ77MinMatch {
			// Write match token
			out.WriteByte(1) // match flag
			binary.Write(&out, binary.LittleEndian, distance)
			binary.Write(&out, binary.LittleEndian, length)
			i += int(length)
		} else {
			// Write literal
			out.WriteByte(0) // literal flag
			out.WriteByte(data[i])
			i++
		}

		// Update window
		comp.addToWindow(data[i-1])
	}

	return bytes.NewReader(out.Bytes()), nil
}

// DecompressLZ77 decompresses LZ77-compressed data
func DecompressLZ77(src io.Reader) ([]byte, error) {
	data, err := io.ReadAll(src)
	if err != nil {
		return nil, err
	}

	r := bytes.NewReader(data)
	var origSize uint32
	if err := binary.Read(r, binary.LittleEndian, &origSize); err != nil {
		return nil, err
	}

	var out bytes.Buffer
	out.Grow(int(origSize))
	window := make([]byte, 0, LZ77WindowSize)

	for out.Len() < int(origSize) {
		var flag byte
		if err := binary.Read(r, binary.LittleEndian, &flag); err != nil {
			break
		}

		if flag == 0 {
			// Literal
			var lit byte
			if err := binary.Read(r, binary.LittleEndian, &lit); err != nil {
				break
			}
			out.WriteByte(lit)
			if len(window) < LZ77WindowSize {
				window = append(window, lit)
			} else {
				window = append(window[1:], lit)
			}
		} else {
			// Match
			var distance uint16
			var length uint16
			if err := binary.Read(r, binary.LittleEndian, &distance); err != nil {
				break
			}
			if err := binary.Read(r, binary.LittleEndian, &length); err != nil {
				break
			}

			// Copy from window
			start := len(window) - int(distance)
			for j := 0; j < int(length); j++ {
				b := window[start+j%int(distance)]
				out.WriteByte(b)
				if len(window) < LZ77WindowSize {
					window = append(window, b)
				} else {
					window = append(window[1:], b)
				}
			}
		}
	}

	return out.Bytes(), nil
}

func (c *LZ77Compressor) findBestMatch(data []byte, pos int) (uint16, uint16) {
	if pos >= len(data)-LZ77MinMatch {
		return 0, 0
	}

	bestDistance := uint16(0)
	bestLength := uint16(0)
	maxLen := len(data) - pos
	if maxLen > int(LZ77MaxMatch) {
		maxLen = int(LZ77MaxMatch)
	}

	// Search in window for matches
	windowStart := 0
	if len(c.window) > LZ77WindowSize {
		windowStart = len(c.window) - LZ77WindowSize
	}

	for i := windowStart; i < len(c.window); i++ {
		// Match attempt
		length := 0
		for length < maxLen && i+length < len(c.window) &&
			c.window[i+length] == data[pos+length] {
			length++
		}

		if length >= LZ77MinMatch && uint16(length) > bestLength {
			bestDistance = uint16(len(c.window) - i)
			bestLength = uint16(length)
		}
	}

	return bestDistance, bestLength
}

func (c *LZ77Compressor) addToWindow(b byte) {
	c.window = append(c.window, b)
	if len(c.window) > LZ77WindowSize {
		c.window = c.window[1:]
	}
}
