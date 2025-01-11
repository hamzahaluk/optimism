package memory

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math/bits"
	"sort"

	"github.com/ethereum-optimism/optimism/cannon/mipsevm/arch"
	"github.com/ethereum/go-ethereum/crypto"
)

const (
	WordSize     = arch.WordSize
	PageSize     = 1 << arch.PageAddrSize
	PageAddrMask = PageSize - 1
)

type Word = arch.Word

type Memory struct {
	nodes        map[uint64]*[32]byte
	pages        map[Word]*CachedPage
	lastPageKeys [2]Word
	lastPage     [2]*CachedPage
}

type CachedPage struct {
	Data *Page
	Valid bool
}

func NewMemory() *Memory {
	return &Memory{
		nodes:        make(map[uint64]*[32]byte),
		pages:        make(map[Word]*CachedPage),
		lastPageKeys: [2]Word{^Word(0), ^Word(0)},
	}
}

// Utility function for hash computation
func hashPair(left, right [32]byte) [32]byte {
	return crypto.Keccak256Hash(left[:], right[:])
}

// SetWord stores a word value at the specified address
func (m *Memory) SetWord(addr Word, value Word) {
	if addr&arch.ExtMask != 0 {
		panic(fmt.Errorf("unaligned memory access: %x", addr))
	}

	pageIndex, offset := addr>>arch.PageAddrSize, addr&PageAddrMask
	page := m.getPageOrAlloc(pageIndex)
	page.Valid = false
	arch.ByteOrderWord.PutWord(page.Data[offset:offset+arch.WordSizeBytes], value)
}

// GetWord retrieves a word value from the specified address
func (m *Memory) GetWord(addr Word) Word {
	if addr&arch.ExtMask != 0 {
		panic(fmt.Errorf("unaligned memory access: %x", addr))
	}

	pageIndex, offset := addr>>arch.PageAddrSize, addr&PageAddrMask
	page, exists := m.pages[pageIndex]
	if !exists || !page.Valid {
		return 0
	}
	return arch.ByteOrderWord.Word(page.Data[offset : offset+arch.WordSizeBytes])
}

// Retrieves or allocates a page for the specified index
func (m *Memory) getPageOrAlloc(pageIndex Word) *CachedPage {
	page, exists := m.pages[pageIndex]
	if !exists {
		page = &CachedPage{Data: new(Page), Valid: false}
		m.pages[pageIndex] = page
	}
	return page
}

// Serialize encodes memory into a writer
func (m *Memory) Serialize(out io.Writer) error {
	if err := binary.Write(out, binary.BigEndian, Word(len(m.pages))); err != nil {
		return err
	}
	pageIndexes := make([]Word, 0, len(m.pages))
	for index := range m.pages {
		pageIndexes = append(pageIndexes, index)
	}
	sort.Slice(pageIndexes, func(i, j int) bool { return pageIndexes[i] < pageIndexes[j] })

	for _, index := range pageIndexes {
		if err := binary.Write(out, binary.BigEndian, index); err != nil {
			return err
		}
		if _, err := out.Write(m.pages[index].Data[:]); err != nil {
			return err
		}
	}
	return nil
}

// Deserialize decodes memory from a reader
func (m *Memory) Deserialize(in io.Reader) error {
	var pageCount Word
	if err := binary.Read(in, binary.BigEndian, &pageCount); err != nil {
		return err
	}

	for i := Word(0); i < pageCount; i++ {
		var pageIndex Word
		if err := binary.Read(in, binary.BigEndian, &pageIndex); err != nil {
			return err
		}
		page := &CachedPage{Data: new(Page), Valid: false}
		if _, err := io.ReadFull(in, page.Data[:]); err != nil {
			return err
		}
		m.pages[pageIndex] = page
	}
	return nil
}

// MerkleRoot computes the Merkle root for the entire memory
func (m *Memory) MerkleRoot() [32]byte {
	return m.computeSubtreeHash(1)
}

// Helper for recursive Merkle tree hash computation
func (m *Memory) computeSubtreeHash(index uint64) [32]byte {
	if hash, exists := m.nodes[index]; exists {
		return *hash
	}
	if index >= uint64(len(m.pages)) {
		return [32]byte{}
	}
	left := m.computeSubtreeHash(index << 1)
	right := m.computeSubtreeHash((index << 1) | 1)
	result := hashPair(left, right)
	m.nodes[index] = &result
	return result
}

// Copy creates a duplicate of the memory
func (m *Memory) Copy() *Memory {
	copy := NewMemory()
	for index, page := range m.pages {
		newPage := &CachedPage{Data: new(Page), Valid: page.Valid}
		copy(*newPage.Data, *page.Data)
		copy.pages[index] = newPage
	}
	return copy
}
