package pkg

import (
	"bytes"
	"container/heap"
	"encoding/binary"
	"io"
)

// Huffman compression using frequency-based encoding

type HuffmanNode struct {
	byte  byte
	freq  int64
	left  *HuffmanNode
	right *HuffmanNode
	index int // for heap
}

type huffmanHeap []*HuffmanNode

func (h huffmanHeap) Len() int           { return len(h) }
func (h huffmanHeap) Less(i, j int) bool { return h[i].freq < h[j].freq }
func (h huffmanHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}
func (h *huffmanHeap) Push(x interface{}) {
	n := len(*h)
	node := x.(*HuffmanNode)
	node.index = n
	*h = append(*h, node)
}
func (h *huffmanHeap) Pop() interface{} {
	old := *h
	n := len(old)
	node := old[n-1]
	*h = old[0 : n-1]
	return node
}

// CompressHuffman compresses data using Huffman coding
func CompressHuffman(src io.Reader) (io.Reader, error) {
	data, err := io.ReadAll(src)
	if err != nil {
		return nil, err
	}

	// Count byte frequencies
	freqs := make(map[byte]int64)
	for _, b := range data {
		freqs[b]++
	}

	if len(freqs) == 0 {
		return bytes.NewReader([]byte{}), nil
	}

	// Build Huffman tree
	h := &huffmanHeap{}
	heap.Init(h)

	for b, freq := range freqs {
		node := &HuffmanNode{byte: b, freq: freq}
		heap.Push(h, node)
	}

	for h.Len() > 1 {
		left := heap.Pop(h).(*HuffmanNode)
		right := heap.Pop(h).(*HuffmanNode)
		parent := &HuffmanNode{
			freq:  left.freq + right.freq,
			left:  left,
			right: right,
		}
		heap.Push(h, parent)
	}

	root := heap.Pop(h).(*HuffmanNode)

	// Generate codes
	codes := make(map[byte][]bool)
	generateCodes(root, codes, nil)

	// Encode
	var out bytes.Buffer

	// Write header: number of unique bytes
	binary.Write(&out, binary.LittleEndian, uint16(len(freqs)))

	// Write frequency table
	for b := byte(0); b < 255; b++ {
		if freq, ok := freqs[b]; ok {
			out.WriteByte(b)
			binary.Write(&out, binary.LittleEndian, freq)
		}
	}

	// Write original size
	binary.Write(&out, binary.LittleEndian, uint64(len(data)))

	// Write encoded data
	var bits []bool
	for _, b := range data {
		bits = append(bits, codes[b]...)
	}

	// Pack bits into bytes
	binary.Write(&out, binary.LittleEndian, uint32(len(bits)))
	bitIdx := 0
	for bitIdx < len(bits) {
		var byte byte
		for i := 0; i < 8 && bitIdx < len(bits); i++ {
			if bits[bitIdx] {
				byte |= 1 << uint(7-i)
			}
			bitIdx++
		}
		out.WriteByte(byte)
	}

	return bytes.NewReader(out.Bytes()), nil
}

// DecompressHuffman decompresses Huffman-encoded data
func DecompressHuffman(src io.Reader) ([]byte, error) {
	data, err := io.ReadAll(src)
	if err != nil {
		return nil, err
	}

	if len(data) == 0 {
		return []byte{}, nil
	}

	r := bytes.NewReader(data)

	// Read number of unique bytes
	var numBytes uint16
	if err := binary.Read(r, binary.LittleEndian, &numBytes); err != nil {
		return nil, err
	}

	// Read frequency table and rebuild tree
	freqs := make(map[byte]int64)
	for i := 0; i < int(numBytes); i++ {
		var b byte
		var freq int64
		if err := binary.Read(r, binary.LittleEndian, &b); err != nil {
			return nil, err
		}
		if err := binary.Read(r, binary.LittleEndian, &freq); err != nil {
			return nil, err
		}
		freqs[b] = freq
	}

	// Rebuild Huffman tree
	h := &huffmanHeap{}
	heap.Init(h)
	for b, freq := range freqs {
		node := &HuffmanNode{byte: b, freq: freq}
		heap.Push(h, node)
	}

	for h.Len() > 1 {
		left := heap.Pop(h).(*HuffmanNode)
		right := heap.Pop(h).(*HuffmanNode)
		parent := &HuffmanNode{
			freq:  left.freq + right.freq,
			left:  left,
			right: right,
		}
		heap.Push(h, parent)
	}

	root := heap.Pop(h).(*HuffmanNode)

	// Read original size
	var origSize uint64
	if err := binary.Read(r, binary.LittleEndian, &origSize); err != nil {
		return nil, err
	}

	// Read bit count
	var bitCount uint32
	if err := binary.Read(r, binary.LittleEndian, &bitCount); err != nil {
		return nil, err
	}

	// Read compressed bits
	var bits []bool
	remaining := bitCount
	for remaining > 0 {
		var b byte
		if err := binary.Read(r, binary.LittleEndian, &b); err != nil {
			break
		}
		for i := 0; i < 8 && remaining > 0; i++ {
			bits = append(bits, (b&(1<<uint(7-i))) != 0)
			remaining--
		}
	}

	// Decode
	var out []byte
	node := root
	for _, bit := range bits {
		if bit {
			node = node.right
		} else {
			node = node.left
		}

		if node.left == nil && node.right == nil {
			out = append(out, node.byte)
			node = root
			if int64(len(out)) >= int64(origSize) {
				break
			}
		}
	}

	return out, nil
}

func generateCodes(node *HuffmanNode, codes map[byte][]bool, prefix []bool) {
	if node == nil {
		return
	}

	if node.left == nil && node.right == nil {
		codes[node.byte] = append([]bool{}, prefix...)
		if len(codes[node.byte]) == 0 {
			// Single unique byte case
			codes[node.byte] = []bool{false}
		}
		return
	}

	newPrefix := append([]bool{}, prefix...)
	newPrefix = append(newPrefix, false)
	generateCodes(node.left, codes, newPrefix)

	newPrefix = append([]bool{}, prefix...)
	newPrefix = append(newPrefix, true)
	generateCodes(node.right, codes, newPrefix)
}
