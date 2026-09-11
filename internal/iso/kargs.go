package iso

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// The bootloader configs reserve a run of padding for the kernel command line,
// described by coreos/kargs.json. The region begins with the arguments
// themselves, then a newline, then padding to the end. Because the padding is
// a comment to both GRUB and isolinux, whatever is left over is ignored.

const kargsPath = "coreos/kargs.json"

type kargsInfo struct {
	Default string `json:"default"`
	Size    int    `json:"size"`
	Files   []struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Pad    string `json:"pad"`
		End    string `json:"end"`
	} `json:"files"`
}

// KernelArgs returns the command line the image currently boots with.
func (im *Image) KernelArgs() (string, error) {
	info, err := im.kargsInfo()
	if err != nil {
		return "", err
	}
	if len(info.Files) == 0 {
		return info.Default, nil
	}

	first := info.Files[0]
	entry, err := im.Find(first.Path)
	if err != nil {
		return "", fmt.Errorf("locate %s: %w", first.Path, err)
	}
	region := make([]byte, info.Size)
	if _, err := im.f.ReadAt(region, entry.Offset()+int64(first.Offset)); err != nil {
		return "", fmt.Errorf("read kernel arguments: %w", err)
	}
	if i := bytes.IndexByte(region, '\n'); i >= 0 {
		region = region[:i]
	}
	return strings.TrimRight(string(region), " "), nil
}

// AppendKernelArgs adds arguments to every bootloader config in the image.
func (im *Image) AppendKernelArgs(extra []string) error {
	if len(extra) == 0 {
		return nil
	}
	current, err := im.KernelArgs()
	if err != nil {
		return err
	}
	return im.SetKernelArgs(current + " " + strings.Join(extra, " "))
}

// SetKernelArgs replaces the command line in every bootloader config.
func (im *Image) SetKernelArgs(args string) error {
	info, err := im.kargsInfo()
	if err != nil {
		return err
	}

	for _, file := range info.Files {
		pad := byte('#')
		if file.Pad != "" {
			pad = file.Pad[0]
		}
		end := byte('\n')
		if file.End != "" {
			end = file.End[0]
		}

		// The arguments, a terminator, then padding for the rest of the run.
		if len(args)+1 > info.Size {
			return fmt.Errorf("kernel command line is %d bytes but the embed area holds %d",
				len(args)+1, info.Size)
		}
		region := bytes.Repeat([]byte{pad}, info.Size)
		copy(region, args)
		region[len(args)] = end

		entry, err := im.Find(file.Path)
		if err != nil {
			return fmt.Errorf("locate %s: %w", file.Path, err)
		}
		if int64(file.Offset)+int64(info.Size) > int64(entry.Size) {
			return fmt.Errorf("%s: kernel argument area runs past the end of the file", file.Path)
		}
		if _, err := im.f.WriteAt(region, entry.Offset()+int64(file.Offset)); err != nil {
			return fmt.Errorf("write kernel arguments to %s: %w", file.Path, err)
		}
	}
	return nil
}

func (im *Image) kargsInfo() (*kargsInfo, error) {
	raw, err := im.ReadFile(kargsPath)
	if err != nil {
		return nil, fmt.Errorf("this image has no kernel argument areas: %w", err)
	}
	var info kargsInfo
	if err := json.Unmarshal(bytes.TrimRight(raw, "\x00"), &info); err != nil {
		return nil, fmt.Errorf("parse %s: %w", kargsPath, err)
	}
	if info.Size <= 0 {
		return nil, fmt.Errorf("%s declares no area size", kargsPath)
	}
	return &info, nil
}

// Region is a byte range within the image.
type Region struct {
	Path   string
	Offset int64
	Length int64
}

// KargAreas returns where the kernel command line is stored, one region per
// bootloader config. Anything comparing a baked image against its input needs
// these, because writing kernel arguments legitimately changes bytes here.
func (im *Image) KargAreas() ([]Region, error) {
	info, err := im.kargsInfo()
	if err != nil {
		return nil, err
	}

	out := make([]Region, 0, len(info.Files))
	for _, file := range info.Files {
		entry, err := im.Find(file.Path)
		if err != nil {
			return nil, fmt.Errorf("locate %s: %w", file.Path, err)
		}
		out = append(out, Region{
			Path:   file.Path,
			Offset: entry.Offset() + int64(file.Offset),
			Length: int64(info.Size),
		})
	}
	return out, nil
}
