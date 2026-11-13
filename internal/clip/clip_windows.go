//go:build windows

package clip

import (
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")

	pOpenClipboard              = user32.NewProc("OpenClipboard")
	pCloseClipboard             = user32.NewProc("CloseClipboard")
	pEmptyClipboard             = user32.NewProc("EmptyClipboard")
	pGetClipboardData           = user32.NewProc("GetClipboardData")
	pSetClipboardData           = user32.NewProc("SetClipboardData")
	pIsClipboardFormatAvailable = user32.NewProc("IsClipboardFormatAvailable")
	pGetClipboardSequenceNumber = user32.NewProc("GetClipboardSequenceNumber")
	pRegisterClipboardFormat    = user32.NewProc("RegisterClipboardFormatW")
	pGlobalAlloc                = kernel32.NewProc("GlobalAlloc")
	pGlobalFree                 = kernel32.NewProc("GlobalFree")
	pGlobalLock                 = kernel32.NewProc("GlobalLock")
	pGlobalUnlock               = kernel32.NewProc("GlobalUnlock")
	pGlobalSize                 = kernel32.NewProc("GlobalSize")
	pDragQueryFile              = shell32.NewProc("DragQueryFileW")
)

const (
	cfUnicodeText  = 13
	cfHDrop        = 15
	gmemMoveable   = 0x0002
	dropEffectCopy = 1
)

type winClipboard struct{}

// New returns the Windows clipboard.
func New() (Clipboard, error) { return winClipboard{}, nil }

func (winClipboard) Describe() string { return "Windows clipboard" }

func (winClipboard) Mark() (string, error) {
	n, _, _ := pGetClipboardSequenceNumber.Call()
	return strconv.FormatUint(uint64(n), 10), nil
}

// ptr converts an address returned by the operating system into a pointer.
func ptr(addr uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&addr)) }

// open opens the clipboard, retrying while another program holds it. The
// clipboard belongs to the thread that opened it, so the goroutine is pinned
// until the returned function is called.
func open() (func(), error) {
	runtime.LockOSThread()
	for i := 0; i < 25; i++ {
		if r, _, _ := pOpenClipboard.Call(0); r != 0 {
			return func() {
				pCloseClipboard.Call()
				runtime.UnlockOSThread()
			}, nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	runtime.UnlockOSThread()
	return nil, errors.New("the clipboard is in use by another program")
}

func (winClipboard) Read() (Content, error) {
	done, err := open()
	if err != nil {
		return Content{}, err
	}
	defer done()

	if ok, _, _ := pIsClipboardFormatAvailable.Call(cfHDrop); ok != 0 {
		h, _, _ := pGetClipboardData.Call(cfHDrop)
		if h != 0 {
			n, _, _ := pDragQueryFile.Call(h, 0xFFFFFFFF, 0, 0)
			var files []string
			for i := uintptr(0); i < n; i++ {
				size, _, _ := pDragQueryFile.Call(h, i, 0, 0)
				buf := make([]uint16, size+1)
				pDragQueryFile.Call(h, i, uintptr(unsafe.Pointer(&buf[0])), size+1)
				files = append(files, syscall.UTF16ToString(buf))
			}
			if len(files) > 0 {
				return Content{Kind: Files, Files: files}, nil
			}
		}
	}
	if ok, _, _ := pIsClipboardFormatAvailable.Call(cfUnicodeText); ok != 0 {
		h, _, _ := pGetClipboardData.Call(cfUnicodeText)
		if h != 0 {
			p, _, _ := pGlobalLock.Call(h)
			if p != 0 {
				defer pGlobalUnlock.Call(h)
				size, _, _ := pGlobalSize.Call(h)
				units := unsafe.Slice((*uint16)(ptr(p)), size/2)
				end := 0
				for end < len(units) && units[end] != 0 {
					end++
				}
				if end > 0 {
					return Content{Kind: Text, Text: string(utf16.Decode(units[:end]))}, nil
				}
			}
		}
	}
	return Content{}, nil
}

// global copies data into a movable global memory block, the form in which
// the clipboard takes ownership of data.
func global(data []byte) (uintptr, error) {
	h, _, err := pGlobalAlloc.Call(gmemMoveable, uintptr(len(data)))
	if h == 0 {
		return 0, fmt.Errorf("GlobalAlloc: %v", err)
	}
	p, _, err := pGlobalLock.Call(h)
	if p == 0 {
		pGlobalFree.Call(h)
		return 0, fmt.Errorf("GlobalLock: %v", err)
	}
	copy(unsafe.Slice((*byte)(ptr(p)), len(data)), data)
	pGlobalUnlock.Call(h)
	return h, nil
}

func utf16Bytes(s string) []byte {
	units := utf16.Encode([]rune(s))
	out := make([]byte, 0, len(units)*2+2)
	for _, u := range units {
		out = append(out, byte(u), byte(u>>8))
	}
	return append(out, 0, 0)
}

// set replaces the clipboard content with the given formats.
func set(formats map[uintptr][]byte) error {
	handles := map[uintptr]uintptr{}
	for format, data := range formats {
		h, err := global(data)
		if err != nil {
			for _, other := range handles {
				pGlobalFree.Call(other)
			}
			return err
		}
		handles[format] = h
	}
	done, err := open()
	if err != nil {
		for _, h := range handles {
			pGlobalFree.Call(h)
		}
		return err
	}
	defer done()
	pEmptyClipboard.Call()
	var firstErr error
	for format, h := range handles {
		if r, _, err := pSetClipboardData.Call(format, h); r == 0 {
			pGlobalFree.Call(h) // on success the clipboard owns the memory
			if firstErr == nil {
				firstErr = fmt.Errorf("SetClipboardData: %v", err)
			}
		}
	}
	return firstErr
}

func (winClipboard) WriteText(text string) error {
	return set(map[uintptr][]byte{cfUnicodeText: utf16Bytes(text)})
}

func (winClipboard) WriteFiles(paths []string) error {
	if len(paths) == 0 {
		return errors.New("no files")
	}
	// DROPFILES header: offset of the file list (20), drop point, non-client
	// flag, wide-character flag; then the paths, each ending in a zero, and
	// one more zero at the very end.
	data := []byte{20, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0}
	for _, p := range paths {
		data = append(data, utf16Bytes(p)...)
	}
	data = append(data, 0, 0)

	// Tell Explorer that pasting should copy the files (not move them).
	name, _ := syscall.UTF16PtrFromString("Preferred DropEffect")
	effect, _, _ := pRegisterClipboardFormat.Call(uintptr(unsafe.Pointer(name)))
	formats := map[uintptr][]byte{cfHDrop: data}
	if effect != 0 {
		formats[effect] = []byte{dropEffectCopy, 0, 0, 0}
	}
	return set(formats)
}
