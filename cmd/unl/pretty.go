package main

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/jasonthorsness/unlurker/hn"
	"github.com/jasonthorsness/unlurker/unl"
)

const (
	colorDarkBlue   = "\033[34m"
	colorDarkGray   = "\033[90m"
	colorLightBlue  = "\033[94m"
	colorLightGreen = "\033[92m"
	colorReset      = "\033[0m"
)

type prettyLine struct {
	link         string
	by           string
	age          string
	indent       string
	text         string
	root         bool
	active       bool
	secondChance bool
}

type prettyWriter struct {
	now           time.Time
	activeAfter   time.Time
	adjustedTimes map[int]int64
	lines         []prettyLine
	maxWidth      int
	showColor     bool
}

func calculateIndent(items []*unl.ItemWithDepth) []string {
	indent := make([]string, len(items))
	lastDepth := 0
	stack := make([]byte, 0, items[len(items)-1].Depth)
	indent[0] = ""

	for i := len(items) - 1; i > 0; i-- {
		item := items[i]

		if item.Depth < lastDepth {
			stack = stack[:len(stack)-1]
		} else {
			if len(stack) > 0 && i < len(items)-1 {
				stack[lastDepth-1] = '|'
			}

			for range item.Depth - lastDepth {
				stack = append(stack, ' ')
			}
		}

		indent[i] = string(stack)
		lastDepth = item.Depth
	}

	return indent
}

func (pw *prettyWriter) writeTree(root *hn.Item, allByParent map[int]hn.ItemSet) {
	flat := unl.FlattenTree(root, allByParent)
	activeMap := unl.BuildActiveMap(flat, pw.activeAfter)
	indent := calculateIndent(flat)

	for i, item := range flat {
		ae := activeMap[item.ID]
		showText := item.Parent == nil || ae != 0
		active := (ae & unl.ActiveMapSelf) > 0

		v, ok := pw.adjustedTimes[item.ID]
		isSecondChance := ok && item.Time != v

		pw.writeItemIndent(item.Item, showText, active, isSecondChance, indent[i])
	}
}

func (pw *prettyWriter) writeItemIndent(
	item *hn.Item, showText bool, isActive bool, isSecondChance bool, indent string,
) {
	link := "https://news.ycombinator.com/item?id=" + strconv.Itoa(item.ID)
	by := item.By

	// Use adjusted time if available, otherwise use original time
	effectiveTime := item.Time
	if adjustedTime, ok := pw.adjustedTimes[item.ID]; ok {
		effectiveTime = adjustedTime
	}

	age := unl.PrettyFormatDuration(pw.now.Sub(time.Unix(effectiveTime, 0)))
	text := ""

	if showText {
		text = unl.PrettyFormatTitle(item, true)
	}

	pw.lines = append(pw.lines, prettyLine{link, by, age, indent, text, item.Parent == nil, isActive, isSecondChance})
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

		if line.secondChance {
			if pw.showColor {
				buf.WriteString(colorDarkGray)
			}

			const spaceBetweenFields = 3
			indentLength := len(line.link) + maxByLength + maxAgeLength + spaceBetweenFields
			indent := strings.Repeat(" ", indentLength)
			buf.WriteString(indent)
			buf.WriteString("↙ time adjusted for second-chance\n")
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

	for _, r := range line.text {
		if remaining == 0 {
			buf.Truncate(buf.Len() - rn)
			buf.WriteRune('…')

			break
		}

		rn, _ = buf.WriteRune(r)
		remaining--
	}
}
