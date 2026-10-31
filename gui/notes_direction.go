package gui

import "strings"

// Fyne's text widgets have no per-paragraph direction, so (as in other plain-text
// editors) the direction of a note is set with Unicode formatting characters:
//
//	RLE (U+202B) ... PDF (U+202C)  forces a whole block right-to-left
//	LRE (U+202A) ... PDF (U+202C)  forces a whole block left-to-right
//	RLM (U+200F) at a line's start nudges that line right-to-left
//	LRM (U+200E) at a line's start nudges that line left-to-right
//
// Everything counts in runes, never bytes, or multi-byte (Arabic) text would be cut
// in the middle of a character.
const (
	rle = '\u202B'
	lre = '\u202A'
	pdf = '\u202C'
	rlm = '\u200F'
	lrm = '\u200E'
)

// toggleWholeDirection flips the direction of the whole text: an existing RLE/LRE ...
// PDF wrapper is replaced by the opposite one, and text without one becomes RTL.
func toggleWholeDirection(text string) string {
	r := []rune(text)
	if len(r) >= 2 && r[len(r)-1] == pdf && (r[0] == rle || r[0] == lre) {
		inner := string(r[1 : len(r)-1])
		if r[0] == rle {
			return string(lre) + inner + string(pdf)
		}
		return string(rle) + inner + string(pdf)
	}
	return string(rle) + text + string(pdf)
}

// toggleLineDirection flips the direction of one line (0-based), by adding or flipping
// its leading mark.
func toggleLineDirection(text string, line int) string {
	lines := strings.Split(text, "\n")
	if line < 0 || line >= len(lines) {
		return text
	}
	r := []rune(lines[line])
	switch {
	case len(r) > 0 && r[0] == rlm:
		lines[line] = string(lrm) + string(r[1:])
	case len(r) > 0 && r[0] == lrm:
		lines[line] = string(rlm) + string(r[1:])
	default:
		lines[line] = string(rlm) + lines[line]
	}
	return strings.Join(lines, "\n")
}
