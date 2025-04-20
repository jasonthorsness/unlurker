package main

import (
	"bytes"
	"fmt"
	"html"
	"io"
	"math"
	"strconv"
	"time"
	"unicode"

	"github.com/jasonthorsness/unlurker/hn"
)

const (
	colorDarkBlue   = "\033[34m"
	colorDarkGray   = "\033[90m"
	colorLightBlue  = "\033[94m"
	colorLightGreen = "\033[92m"
	colorReset      = "\033[0m"
)

type prettyLine struct {
	link   string
	by     string
	age    string
	indent string
	text   string
	root   bool
	active bool
}

type prettyWriter struct {
	now         time.Time
	activeAfter time.Time
	lines       []prettyLine
	showColor   bool
	maxWidth    int
}

func (pw *prettyWriter) writeTree(item *hn.Item, allByParent map[int]hn.ItemSet) {
	pw.writeTreeRecurse(item, allByParent, "")
}

func (pw *prettyWriter) writeTreeRecurse(item *hn.Item, allByParent map[int]hn.ItemSet, indent string) {
	isActive := time.Unix(item.Time, 0).After(pw.activeAfter) && !item.Dead && !item.Deleted
	hasActiveChild := findActiveChild(item, allByParent, pw.activeAfter)

	pw.writeItemIndent(item, isActive || hasActiveChild || item.Parent == nil, isActive, indent)

	children := allByParent[item.ID]
	cc := children.Slice()

	for i, child := range cc {
		var childIndent string

		if i != len(cc)-1 {
			childIndent = indent + "|"
		} else {
			childIndent = indent + " "
		}

		pw.writeTreeRecurse(child, allByParent, childIndent)
	}
}

func findActiveChild(item *hn.Item, allByParent map[int]hn.ItemSet, activeAfter time.Time) bool {
	for _, child := range allByParent[item.ID] {
		if time.Unix(child.Time, 0).After(activeAfter) && !child.Dead && !child.Deleted {
			return true
		}
	}

	return false
}

func (pw *prettyWriter) writeItemIndent(item *hn.Item, showText bool, isActive bool, indent string) {
	link := "https://news.ycombinator.com/item?id=" + strconv.Itoa(item.ID)
	by := item.By
	age := prettyFormatDuration(pw.now.Sub(time.Unix(item.Time, 0)))
	text := ""

	if showText {
		switch {
		case item.Dead:
			text = "[dead]"
		case item.Deleted:
			text = "[deleted]"
		case item.Title != "":
			text = item.Title
		default:
			text = item.Text
		}
	}

	pw.lines = append(pw.lines, prettyLine{link, by, age, indent, text, item.Parent == nil, isActive})
}

func (pw *prettyWriter) WriteTo(w io.Writer) (int64, error) {
	maxByLength := 0
	maxAgeLength := 0

	for _, line := range pw.lines {
		maxByLength = max(len(line.by), maxByLength)
		maxAgeLength = max(len(line.age), maxAgeLength)
	}

	var n int64
	var buf bytes.Buffer

	for i, line := range pw.lines {
		buf.Reset()

		if line.root && i != 0 {
			buf.WriteString("\n")
		}

		if pw.showColor {
			buf.WriteString(colorReset)
		}

		buf.WriteString(line.link)

		printable := len(line.link)

		printable += writeToBy(&buf, &line, maxByLength)

		printable += writeToAge(&buf, &line, maxAgeLength, pw.showColor)

		printable += writeToIndent(&buf, &line, pw.showColor)

		writeToText(&buf, &line, pw.showColor, pw.maxWidth, printable)

		buf.WriteString("\n")

		nn, err := buf.WriteTo(w)
		if err != nil {
			return 0, fmt.Errorf("failed to write to writer: %w", err)
		}

		n += nn
	}

	return n, nil
}

func writeToBy(buf *bytes.Buffer, line *prettyLine, maxByLength int) int {
	buf.WriteByte(' ')

	for range maxByLength - len(line.by) {
		buf.WriteByte(' ')
	}

	buf.WriteString(line.by)

	return maxByLength + 1
}

func writeToAge(buf *bytes.Buffer, line *prettyLine, maxAgeLength int, showColor bool) int {
	if showColor {
		if line.active {
			buf.WriteString(colorLightBlue)
		} else {
			buf.WriteString(colorDarkBlue)
		}
	}

	buf.WriteByte(' ')

	for range maxAgeLength - len(line.age) {
		buf.WriteByte(' ')
	}

	buf.WriteString(line.age)

	return maxAgeLength + 1
}

func writeToIndent(buf *bytes.Buffer, line *prettyLine, showColor bool) int {
	if showColor {
		buf.WriteString(colorDarkGray)
	}

	printable := len(line.indent) + 1

	buf.WriteByte(' ')
	buf.WriteString(line.indent)

	if line.indent != "" {
		buf.WriteString("\\")

		printable++

		if line.text != "" {
			buf.WriteString("- ")

			printable += 2
		}
	}

	return printable
}

func writeToText(buf *bytes.Buffer, line *prettyLine, showColor bool, maxWidth int, printable int) {
	if showColor {
		if line.root {
			buf.WriteString(colorLightGreen)
		} else {
			buf.WriteString(colorReset)
		}
	}

	remaining := math.MaxInt

	if maxWidth > 0 {
		remaining = max(1, maxWidth-printable)
	}

	var rn int

	for _, r := range html.UnescapeString(line.text) {
		if remaining == 0 {
			buf.Truncate(buf.Len() - rn)
			buf.WriteRune('…')

			break
		}

		if !unicode.IsPrint(r) {
			r = ' '
		}

		rn, _ = buf.WriteRune(r)
		remaining--
	}
}

// prettyFormatDuration formats a positive duration for columnar display.
// Output will align in columns if left-padded.
func prettyFormatDuration(d time.Duration) string {
	totalMinutes := int(d.Minutes())

	const minutesPerHour = 60

	if totalMinutes < minutesPerHour {
		return fmt.Sprintf("%dm", totalMinutes)
	}

	hours := totalMinutes / minutesPerHour
	minutes := totalMinutes % minutesPerHour

	return fmt.Sprintf("%dh%2dm", hours, minutes)
}
