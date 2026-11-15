package rview

import (
	"bytes"
	"image"
)

// tileSize is the side of the squares a picture is compared in. Only the
// squares that changed since the last picture are sent.
const tileSize = 32

// maxRects limits how many rectangles one update is cut into; beyond that
// one rectangle around everything is cheaper than many small pictures.
const maxRects = 48

func tilesFor(w, h int) (tw, th int) {
	return (w + tileSize - 1) / tileSize, (h + tileSize - 1) / tileSize
}

// changedTiles marks, in dirty, the tiles in which cur differs from prev.
// Both are RGBA pictures of w x h pixels. It returns how many changed.
func changedTiles(prev, cur []byte, w, h int, dirty []bool) int {
	tw, th := tilesFor(w, h)
	n := 0
	for ty := 0; ty < th; ty++ {
		y0, y1 := ty*tileSize, min((ty+1)*tileSize, h)
		for tx := 0; tx < tw; tx++ {
			x0, x1 := tx*tileSize, min((tx+1)*tileSize, w)
			changed := false
			for y := y0; y < y1 && !changed; y++ {
				a, b := (y*w+x0)*4, (y*w+x1)*4
				changed = !bytes.Equal(prev[a:b], cur[a:b])
			}
			dirty[ty*tw+tx] = changed
			if changed {
				n++
			}
		}
	}
	return n
}

// rectsOf turns marked tiles into rectangles: runs of tiles in a row, joined
// with the same run in the rows below.
func rectsOf(marked []bool, w, h int) []image.Rectangle {
	tw, th := tilesFor(w, h)
	var out []image.Rectangle
	open := map[[2]int]int{} // a run's first and last tile -> its rectangle in out, while it can still grow
	for ty := 0; ty < th; ty++ {
		next := map[[2]int]int{}
		for tx := 0; tx < tw; {
			if !marked[ty*tw+tx] {
				tx++
				continue
			}
			start := tx
			for tx < tw && marked[ty*tw+tx] {
				tx++
			}
			run := [2]int{start, tx}
			y1 := min((ty+1)*tileSize, h)
			if i, ok := open[run]; ok {
				out[i].Max.Y = y1
				next[run] = i
				continue
			}
			out = append(out, image.Rect(start*tileSize, ty*tileSize, min(tx*tileSize, w), y1))
			next[run] = len(out) - 1
		}
		open = next
	}
	if len(out) > maxRects {
		box := out[0]
		for _, r := range out[1:] {
			box = box.Union(r)
		}
		return []image.Rectangle{box}
	}
	return out
}

// area is the number of pixels the rectangles cover.
func area(rects []image.Rectangle) int {
	n := 0
	for _, r := range rects {
		n += r.Dx() * r.Dy()
	}
	return n
}

// markTiles sets the tiles touched by the rectangles to v.
func markTiles(tiles []bool, rects []image.Rectangle, w, h int, v bool) {
	tw, _ := tilesFor(w, h)
	for _, r := range rects {
		for ty := r.Min.Y / tileSize; ty*tileSize < r.Max.Y; ty++ {
			for tx := r.Min.X / tileSize; tx*tileSize < r.Max.X; tx++ {
				tiles[ty*tw+tx] = v
			}
		}
	}
}
