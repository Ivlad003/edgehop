//go:build darwin

package clip

import (
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"sync"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

// The macOS clipboard is NSPasteboard. It is reached through the Objective-C
// runtime with purego, so no cgo is needed.

var (
	macOnce sync.Once
	macErr  error

	clsPasteboard, clsString, clsMutableArray, clsURL, clsPool objc.Class

	selGeneral         = objc.RegisterName("generalPasteboard")
	selChangeCount     = objc.RegisterName("changeCount")
	selClearContents   = objc.RegisterName("clearContents")
	selStringForType   = objc.RegisterName("stringForType:")
	selSetString       = objc.RegisterName("setString:forType:")
	selReadObjects     = objc.RegisterName("readObjectsForClasses:options:")
	selWriteObjects    = objc.RegisterName("writeObjects:")
	selAlloc           = objc.RegisterName("alloc")
	selInit            = objc.RegisterName("init")
	selInitWithUTF8    = objc.RegisterName("initWithUTF8String:")
	selRelease         = objc.RegisterName("release")
	selDrain           = objc.RegisterName("drain")
	selUTF8String      = objc.RegisterName("UTF8String")
	selArray           = objc.RegisterName("array")
	selAddObject       = objc.RegisterName("addObject:")
	selCount           = objc.RegisterName("count")
	selObjectAtIndex   = objc.RegisterName("objectAtIndex:")
	selFileURLWithPath = objc.RegisterName("fileURLWithPath:")
	selIsFileURL       = objc.RegisterName("isFileURL")
	selPath            = objc.RegisterName("path")

	typeString objc.ID // NSPasteboardTypeString
)

func macInit() error {
	macOnce.Do(func() {
		defer func() {
			if r := recover(); r != nil {
				macErr = fmt.Errorf("cannot use the macOS pasteboard: %v", r)
			}
		}()
		for _, lib := range []string{
			"/System/Library/Frameworks/Foundation.framework/Foundation",
			"/System/Library/Frameworks/AppKit.framework/AppKit",
		} {
			if _, err := purego.Dlopen(lib, purego.RTLD_GLOBAL|purego.RTLD_LAZY); err != nil {
				macErr = err
				return
			}
		}
		clsPasteboard = objc.GetClass("NSPasteboard")
		clsString = objc.GetClass("NSString")
		clsMutableArray = objc.GetClass("NSMutableArray")
		clsURL = objc.GetClass("NSURL")
		clsPool = objc.GetClass("NSAutoreleasePool")
		if clsPasteboard == 0 || clsString == 0 || clsMutableArray == 0 || clsURL == 0 || clsPool == 0 {
			macErr = errors.New("the macOS pasteboard classes are not available")
			return
		}
		typeString = newString("public.utf8-plain-text") // kept for the life of the program
	})
	return macErr
}

// newString creates an NSString the caller owns (release it when done).
func newString(s string) objc.ID {
	return objc.ID(clsString).Send(selAlloc).Send(selInitWithUTF8, s)
}

func goString(ns objc.ID) string {
	if ns == 0 {
		return ""
	}
	return objc.Send[string](ns, selUTF8String)
}

// withPool runs fn on one OS thread inside an autorelease pool, which is what
// AppKit expects of every caller.
func withPool(fn func(pb objc.ID)) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pool := objc.ID(clsPool).Send(selAlloc).Send(selInit)
	defer pool.Send(selDrain)
	fn(objc.ID(clsPasteboard).Send(selGeneral))
}

type macClipboard struct{}

// New returns the macOS clipboard.
func New() (Clipboard, error) {
	if err := macInit(); err != nil {
		return nil, err
	}
	return macClipboard{}, nil
}

func (macClipboard) Describe() string { return "macOS pasteboard" }

func (macClipboard) Mark() (string, error) {
	var n int
	withPool(func(pb objc.ID) { n = objc.Send[int](pb, selChangeCount) })
	return strconv.Itoa(n), nil
}

func (macClipboard) Read() (Content, error) {
	var c Content
	withPool(func(pb objc.ID) {
		// Files first: a copied file also offers its name as text.
		classes := objc.ID(clsMutableArray).Send(selArray)
		classes.Send(selAddObject, objc.ID(clsURL))
		urls := pb.Send(selReadObjects, classes, objc.ID(0))
		if urls != 0 {
			n := objc.Send[int](urls, selCount)
			for i := 0; i < n; i++ {
				u := urls.Send(selObjectAtIndex, i)
				if u != 0 && objc.Send[bool](u, selIsFileURL) {
					if p := goString(u.Send(selPath)); p != "" {
						c.Files = append(c.Files, p)
					}
				}
			}
		}
		if len(c.Files) > 0 {
			c.Kind = Files
			return
		}
		if text := goString(pb.Send(selStringForType, typeString)); text != "" {
			c.Kind, c.Text = Text, text
		}
	})
	return c, nil
}

func (macClipboard) WriteText(text string) error {
	ok := false
	withPool(func(pb objc.ID) {
		s := newString(text)
		if s == 0 {
			return
		}
		defer s.Send(selRelease)
		pb.Send(selClearContents)
		ok = objc.Send[bool](pb, selSetString, s, typeString)
	})
	if !ok {
		return errors.New("the pasteboard did not accept the text")
	}
	return nil
}

func (macClipboard) WriteFiles(paths []string) error {
	if len(paths) == 0 {
		return errors.New("no files")
	}
	ok := false
	withPool(func(pb objc.ID) {
		urls := objc.ID(clsMutableArray).Send(selArray)
		for _, p := range paths {
			s := newString(p)
			if s == 0 {
				continue
			}
			if u := objc.ID(clsURL).Send(selFileURLWithPath, s); u != 0 {
				urls.Send(selAddObject, u)
			}
			s.Send(selRelease)
		}
		pb.Send(selClearContents)
		ok = objc.Send[bool](pb, selWriteObjects, urls)
	})
	if !ok {
		return errors.New("the pasteboard did not accept the files")
	}
	return nil
}
