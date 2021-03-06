package daosfs

import (
	"encoding/binary"
	"syscall"
	"time"

	"github.com/daos-stack/go-daos/pkg/daos"
	"github.com/intel-hpdd/logging/debug"
	"github.com/pkg/errors"
)

// FileHandle encapsulates functionality for performing file i/o
type FileHandle struct {
	node *DaosNode

	Flags uint32
}

func NewFileHandle(node *DaosNode, flags uint32) *FileHandle {
	return &FileHandle{
		node:  node,
		Flags: flags,
	}
}

func (fh *FileHandle) Write(offset int64, data []byte) (int64, error) {
	var curSize uint64
	if fh.Flags&syscall.O_APPEND > 0 {
		var err error
		curSize, err = fh.node.getSize()
		if err != nil {
			return 0, err
		}
		offset = int64(curSize)
	}
	if err := fh.node.openObjectLatest(); err != nil {
		return 0, err
	}
	defer fh.node.closeObject()

	epoch, err := fh.node.fs.ch.EpochHold(0)
	if err != nil {
		return 0, errors.Wrap(err, "Unable to hold epoch")
	}
	tx := fh.node.fs.ch.EpochDiscard
	defer func() {
		tx(epoch)
	}()

	debug.Printf("Writing %d bytes @ offset %d to %s (%s)", len(data), offset, fh.node.oid, fh.node.Name)

	var keys []*daos.KeyRequest
	keys = append(keys, daos.NewKeyRequest([]byte("Data")))
	keys[0].Put(uint64(offset), uint64(len(data)), 1, data)

	keys = append(keys, daos.NewKeyRequest([]byte("Size")))
	buf := make([]byte, 8)
	binary.LittleEndian.PutUint64(buf, uint64(curSize+uint64(len(data))))
	keys[1].Put(0, 1, 8, buf)

	mtime, err := time.Now().MarshalBinary()
	if err != nil {
		return 0, errors.Wrap(err, "Failed to marshal time.Now()")
	}
	keys = append(keys, daos.NewKeyRequest([]byte("Mtime")))
	keys[2].Put(0, 1, uint64(len(mtime)), mtime)

	tx = fh.node.fs.ch.EpochCommit

	return int64(len(data)), fh.node.oh.Update(epoch, []byte("."), keys)
}

func (fh *FileHandle) Read(offset, size int64, data *[]byte) error {
	actualSize, err := fh.node.getSize()
	if err != nil {
		return err
	}
	if size > int64(actualSize) {
		size = int64(actualSize)
	}
	if err := fh.node.openObjectLatest(); err != nil {
		return err
	}
	defer fh.node.closeObject()

	debug.Printf("Reading %d bytes @ offset %d from %s (%s)", size, offset, fh.node.oid, fh.node.Name)

	var keys []*daos.KeyRequest
	keys = append(keys, daos.NewKeyRequest([]byte("Data")))

	// FIXME: Need to fix the go-daos API in order to give it the data
	// slice we're given and avoid the copy.
	keys[0].Get(uint64(offset), uint64(size), 1)
	if err := fh.node.oh.Fetch(daos.EpochMax, []byte("."), keys); err != nil {
		return err
	}
	*data = append(*data, keys[0].Buffers[0]...)

	return nil
}
