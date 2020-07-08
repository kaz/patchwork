package patchwork

import (
	"bytes"
	"fmt"
	"io"
	"sort"
	"strings"
)

const (
	SECTOR_SIZE uint32 = 2 * 1024
)

type (
	// Device is data layer which Image write updated contents to and read original contents from.
	// You can use os.File as Device.
	Device interface {
		io.Seeker
		io.ReaderAt
		io.WriterAt
	}

	// Image represents rewritable ISO9660 disk image.
	Image struct {
		dev Device
	}
)

// Create instance which has specified device in it.
func NewImage(dev Device) *Image {
	return &Image{dev}
}

func (img *Image) setExtent(r *directoryRecord, buf []byte) error {
	if n, err := img.dev.WriteAt(buf, int64(r.ExtentLocation*SECTOR_SIZE)); err != nil {
		return fmt.Errorf("failed to write data to image: %w", err)
	} else if n != len(buf) {
		return fmt.Errorf("failed to write enough data: expected %v bytes to write, but actual %v bytes could be written", len(buf), n)
	}
	return nil
}
func (img *Image) getExtent(r *directoryRecord) ([]byte, error) {
	buf := make([]byte, r.ExtentSize)
	if n, err := img.dev.ReadAt(buf, int64(r.ExtentLocation*SECTOR_SIZE)); err != nil {
		return nil, fmt.Errorf("failed to read data from image: %w", err)
	} else if n != len(buf) {
		return nil, fmt.Errorf("failed to read enough data: expected %v bytes to read, but actual %v bytes could be read", len(buf), n)
	}
	return buf, nil
}

func (img *Image) setChildren(parent *directoryRecord, children []*directoryRecord) error {
	sort.Slice(children, func(i, j int) bool {
		return strings.Compare(children[i].Identifier, children[j].Identifier) == -1
	})

	buf := make([]byte, 0, parent.ExtentSize)
	for _, child := range children {
		buf = append(buf, child.marshal()...)
	}

	paddingSize := int(parent.ExtentSize) - len(buf)
	if paddingSize < 0 {
		return fmt.Errorf("sector is full: exceeds %v bytes", -paddingSize)
	}

	buf = append(buf, bytes.Repeat([]byte{0}, paddingSize)...)

	if err := img.setExtent(parent, buf); err != nil {
		return fmt.Errorf("failed to set extent: %w", err)
	}
	return nil
}
func (img *Image) getChildren(r *directoryRecord) ([]*directoryRecord, error) {
	buf, err := img.getExtent(r)
	if err != nil {
		return nil, fmt.Errorf("failed to get extent: %w", err)
	}

	children := []*directoryRecord{}
	for i := uint32(0); buf[i] > 0; i += uint32(buf[i]) {
		// Each record has its size at first byte.
		children = append(children, unmarshalDirectoryRecord(buf[i:i+uint32(buf[i])]))
	}
	return children, nil
}

func (img *Image) getVolumeDescriptor() ([]byte, error) {
	buf := make([]byte, SECTOR_SIZE)

	// First 16 sector is reserved area. Next some sector can be volume descriptor.
	for i := uint32(0); ; i++ {
		if n, err := img.dev.ReadAt(buf, int64((i+16)*SECTOR_SIZE)); err != nil {
			return nil, fmt.Errorf("failed to read sector from image: %w", err)
		} else if n != len(buf) {
			return nil, fmt.Errorf("failed to read enough data: expected %v bytes to read, but actual %v bytes could be read", len(buf), n)
		}

		// Volume descriptor type codes, which is located in first byte of sector, is volume descriptor set ternimator.
		if buf[0] == 255 {
			break
		}

		// Type code 1 (Primary) or 2 (Supplementary) is fine.
		if buf[0] == 1 || buf[0] == 2 {
			return buf, nil
		}
	}
	return nil, fmt.Errorf("volume descriptor was not found")
}

func (img *Image) getRootDirectoryRecord() (*directoryRecord, error) {
	vd, err := img.getVolumeDescriptor()
	if err != nil {
		return nil, fmt.Errorf("failed to read volume descriptor: %w", err)
	}

	// DirectoryRecord of root directory is located in [156:190] of volume descriptor
	return unmarshalDirectoryRecord(vd[156:190]), nil
}

func (img *Image) findRecordFromChildren(pwd *directoryRecord, key string) (*directoryRecord, []*directoryRecord, error) {
	children, err := img.getChildren(pwd)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to find children: %w", err)
	}

	for _, child := range children {
		if key == child.RockRidgeName {
			return child, children, nil
		}
	}

	return nil, children, fmt.Errorf("no such entry: %v", key)
}
func (img *Image) findDirectoryRecord(path string) (*directoryRecord, error) {
	dirs := strings.Split(path, "/")
	if len(dirs) < 1 || dirs[0] != "" {
		return nil, fmt.Errorf("unexpected path: %v", path)
	}

	pwd, err := img.getRootDirectoryRecord()
	if err != nil {
		return nil, fmt.Errorf("failed to find root: %w", err)
	}

	for _, dir := range dirs[1:] {
		pwd, _, err = img.findRecordFromChildren(pwd, dir)
		if err != nil {
			return nil, fmt.Errorf("failed to find: %w", err)
		}
	}
	return pwd, nil
}

// UpdateFile updates file in image.
//
// path is a file which will be replaced. It must be a path on RockRidge extention and a valid and existent filename on image's filesystem. (e.g. /EFI/BOOT/grub.cfg)
//
// id is new filename, which is used when the image read as raw-ISO9660 filesystem.
//
// name is also new filename, which is used when the image read as ISO9660 with RockRidge extention.
//
// data is a content which will be written.
func (img *Image) UpdateFile(path, id, name string, data []byte) error {
	dirs := strings.Split(path, "/")

	parent, err := img.findDirectoryRecord(strings.Join(dirs[:len(dirs)-1], "/"))
	if err != nil {
		return fmt.Errorf("failed to find parent directory: %w", err)
	}

	target, children, err := img.findRecordFromChildren(parent, dirs[len(dirs)-1])
	if err != nil {
		return fmt.Errorf("failed to find target: %w", err)
	}

	target.Identifier = id
	target.RockRidgeName = name

	loc, err := img.dev.Seek(0, io.SeekEnd)
	if err != nil {
		return fmt.Errorf("failed to seek device: %w", err)
	}

	target.ExtentSize = uint32(len(data))
	target.ExtentLocation = uint32(loc) / SECTOR_SIZE

	if len(data)%int(SECTOR_SIZE) != 0 {
		data = append(data, bytes.Repeat([]byte{0}, int(SECTOR_SIZE)-len(data)%int(SECTOR_SIZE))...)
	}

	if _, err := img.dev.WriteAt(data, loc); err != nil {
		return fmt.Errorf("failed to write file data: %w", err)
	}

	if err := img.setChildren(parent, children); err != nil {
		return fmt.Errorf("failed to update directory record: %w", err)
	}

	return nil
}
