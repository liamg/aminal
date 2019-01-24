package buffer

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/url"
	"os"
	"time"
)

type Buffer struct {
	lines                 []Line
	cursorX               uint16
	cursorY               uint16
	viewHeight            uint16
	viewWidth             uint16
	cursorAttr            CellAttributes
	displayChangeHandlers []chan bool
	savedX                uint16
	savedY                uint16
	scrollLinesFromBottom uint
	topMargin             uint // see DECSTBM docs - this is for scrollable regions
	bottomMargin          uint // see DECSTBM docs - this is for scrollable regions
	replaceMode           bool // overwrite character at cursor or insert new
	originMode            bool // see DECOM docs - whether cursor is positioned within the margins or not
	lineFeedMode          bool
	autoWrap              bool
	dirty                 bool
	selectionStart        *Position
	selectionEnd          *Position
	selectionComplete     bool // whether the selected text can update or whether it is final
	selectionExpanded     bool // whether the selection to word expansion has already run on this point
	selectionClickTime    time.Time
	defaultCell           Cell
	maxLines              uint64
}

type Position struct {
	Line int
	Col  int
}

// NewBuffer creates a new terminal buffer
func NewBuffer(viewCols uint16, viewLines uint16, attr CellAttributes, maxLines uint64) *Buffer {
	b := &Buffer{
		cursorX:     0,
		cursorY:     0,
		lines:       []Line{},
		cursorAttr:  attr,
		autoWrap:    true,
		defaultCell: Cell{attr: attr},
		maxLines:    maxLines,
	}
	b.SetVerticalMargins(0, uint(viewLines-1))
	b.ResizeView(viewCols, viewLines)
	return b
}

func (buffer *Buffer) GetURLAtPosition(col uint16, viewRow uint16) string {

	row := buffer.convertViewLineToRawLine((viewRow)) - uint64(buffer.scrollLinesFromBottom)

	cell := buffer.GetRawCell(col, row)
	if cell == nil || cell.Rune() == 0x00 {
		return ""
	}

	candidate := ""

	for i := col; i >= 0; i-- {
		cell := buffer.GetRawCell(i, row)
		if cell == nil {
			break
		}
		if isRuneURLSelectionMarker(cell.Rune()) {
			break
		}
		candidate = fmt.Sprintf("%c%s", cell.Rune(), candidate)
	}

	for i := col + 1; i < buffer.viewWidth; i++ {
		cell := buffer.GetRawCell(i, row)
		if cell == nil {
			break
		}
		if isRuneURLSelectionMarker(cell.Rune()) {
			break
		}
		candidate = fmt.Sprintf("%s%c", candidate, cell.Rune())
	}

	if candidate == "" || candidate[0] == '/' {
		return ""
	}

	// check if url
	_, err := url.ParseRequestURI(candidate)
	if err != nil {
		return ""
	}
	return candidate
}

func (buffer *Buffer) SelectWordAtPosition(col uint16, viewRow uint16) {

	row := buffer.convertViewLineToRawLine(viewRow) - uint64(buffer.scrollLinesFromBottom)

	cell := buffer.GetRawCell(col, row)
	if cell == nil || cell.Rune() == 0x00 {
		return
	}

	start := col
	end := col

	for i := col; i >= 0; i-- {
		cell := buffer.GetRawCell(i, row)
		if cell == nil {
			break
		}
		if isRuneWordSelectionMarker(cell.Rune()) {
			break
		}
		start = i
	}

	for i := col; i < buffer.viewWidth; i++ {
		cell := buffer.GetRawCell(i, row)
		if cell == nil {
			break
		}
		if isRuneWordSelectionMarker(cell.Rune()) {
			break
		}
		end = i
	}

	buffer.selectionStart = &Position{
		Col:  int(start),
		Line: int(row),
	}
	buffer.selectionEnd = &Position{
		Col:  int(end),
		Line: int(row),
	}
	buffer.emitDisplayChange()

}

// bounds for word selection
func isRuneWordSelectionMarker(r rune) bool {
	switch r {
	case ',', ' ', ':', ';', 0, '\'', '"', '[', ']', '(', ')', '{', '}':
		return true
	}

	return false
}

func isRuneURLSelectionMarker(r rune) bool {
	switch r {
	case ' ', 0, '\'', '"', '{', '}':
		return true
	}

	return false
}

func (buffer *Buffer) GetSelectedText() string {
	if buffer.selectionStart == nil || buffer.selectionEnd == nil {
		return ""
	}

	text := ""

	var x1, x2, y1, y2 int

	if buffer.selectionStart.Line > buffer.selectionEnd.Line || (buffer.selectionStart.Line == buffer.selectionEnd.Line && buffer.selectionStart.Col > buffer.selectionEnd.Col) {
		y2 = buffer.selectionStart.Line
		y1 = buffer.selectionEnd.Line
		x2 = buffer.selectionStart.Col
		x1 = buffer.selectionEnd.Col
	} else {
		y1 = buffer.selectionStart.Line
		y2 = buffer.selectionEnd.Line
		x1 = buffer.selectionStart.Col
		x2 = buffer.selectionEnd.Col
	}

	for row := y1; row <= y2; row++ {

		if row >= len(buffer.lines) {
			break
		}

		line := buffer.lines[row]

		minX := 0
		maxX := int(buffer.viewWidth) - 1
		if row == y1 {
			minX = x1
		} else if !line.wrapped {
			text += "\n"
		}
		if row == y2 {
			maxX = x2
		}

		for col := minX; col <= maxX; col++ {
			if col >= len(line.cells) {
				break
			}
			cell := line.cells[col]
			text += string(cell.Rune())
		}

	}

	return text
}

func (buffer *Buffer) StartSelection(col uint16, viewRow uint16) {
	row := buffer.convertViewLineToRawLine(viewRow) - uint64(buffer.scrollLinesFromBottom)
	if buffer.selectionComplete {
		buffer.selectionEnd = nil

		if buffer.selectionStart != nil && time.Since(buffer.selectionClickTime) < time.Millisecond*500 {
			if buffer.selectionExpanded {
				//select whole line!
				buffer.selectionStart = &Position{
					Col:  0,
					Line: int(row),
				}
				buffer.selectionEnd = &Position{
					Col:  int(buffer.ViewWidth() - 1),
					Line: int(row),
				}
				buffer.emitDisplayChange()
			} else {
				buffer.SelectWordAtPosition(col, viewRow)
				buffer.selectionExpanded = true
			}
			return
		}

		buffer.selectionExpanded = false
	}

	buffer.selectionComplete = false
	buffer.selectionStart = &Position{
		Col:  int(col),
		Line: int(row),
	}
	buffer.selectionClickTime = time.Now()
}

func (buffer *Buffer) EndSelection(col uint16, viewRow uint16, complete bool) {

	if buffer.selectionComplete {
		return
	}

	buffer.selectionComplete = complete

	defer buffer.emitDisplayChange()

	if buffer.selectionStart == nil {
		buffer.selectionEnd = nil
		return
	}

	row := buffer.convertViewLineToRawLine(viewRow) - uint64(buffer.scrollLinesFromBottom)

	if int(col) == buffer.selectionStart.Col && int(row) == int(buffer.selectionStart.Line) && complete {
		return
	}

	buffer.selectionEnd = &Position{
		Col:  int(col),
		Line: int(row),
	}
}

func (buffer *Buffer) InSelection(col uint16, row uint16) bool {

	if buffer.selectionStart == nil || buffer.selectionEnd == nil {
		return false
	}

	var x1, x2, y1, y2 int

	// first, let's put the selection points in the correct order, earliest first
	if buffer.selectionStart.Line > buffer.selectionEnd.Line || (buffer.selectionStart.Line == buffer.selectionEnd.Line && buffer.selectionStart.Col > buffer.selectionEnd.Col) {
		y2 = buffer.selectionStart.Line
		y1 = buffer.selectionEnd.Line
		x2 = buffer.selectionStart.Col
		x1 = buffer.selectionEnd.Col
	} else {
		y1 = buffer.selectionStart.Line
		y2 = buffer.selectionEnd.Line
		x1 = buffer.selectionStart.Col
		x2 = buffer.selectionEnd.Col
	}

	rawY := int(buffer.convertViewLineToRawLine(row) - uint64(buffer.scrollLinesFromBottom))
	return (rawY > y1 || (rawY == y1 && int(col) >= x1)) && (rawY < y2 || (rawY == y2 && int(col) <= x2))
}

func (buffer *Buffer) IsDirty() bool {
	if !buffer.dirty {
		return false
	}
	buffer.dirty = false
	return true
}

func (buffer *Buffer) SetAutoWrap(enabled bool) {
	buffer.autoWrap = enabled
}

func (buffer *Buffer) IsAutoWrap() bool {
	return buffer.autoWrap
}

func (buffer *Buffer) SetOriginMode(enabled bool) {
	buffer.originMode = enabled
	buffer.SetPosition(0, 0)
}

func (buffer *Buffer) SetInsertMode() {
	buffer.replaceMode = false
}

func (buffer *Buffer) SetReplaceMode() {
	buffer.replaceMode = true
}

func (buffer *Buffer) SetVerticalMargins(top uint, bottom uint) {
	buffer.topMargin = top
	buffer.bottomMargin = bottom
}

// ResetVerticalMargins resets margins to extreme positions
func (buffer *Buffer) ResetVerticalMargins() {
	buffer.SetVerticalMargins(0, uint(buffer.viewHeight-1))
}

func (buffer *Buffer) GetScrollOffset() uint {
	return buffer.scrollLinesFromBottom
}

func (buffer *Buffer) HasScrollableRegion() bool {
	return buffer.topMargin > 0 || buffer.bottomMargin < uint(buffer.ViewHeight())-1
}

func (buffer *Buffer) InScrollableRegion() bool {
	return buffer.HasScrollableRegion() && uint(buffer.cursorY) >= buffer.topMargin && uint(buffer.cursorY) <= buffer.bottomMargin
}

func (buffer *Buffer) ScrollDown(lines uint16) {

	defer buffer.emitDisplayChange()

	if buffer.Height() < int(buffer.ViewHeight()) {
		return
	}

	if uint(lines) > buffer.scrollLinesFromBottom {
		lines = uint16(buffer.scrollLinesFromBottom)
	}
	buffer.scrollLinesFromBottom -= uint(lines)
}

func (buffer *Buffer) ScrollUp(lines uint16) {

	defer buffer.emitDisplayChange()

	if buffer.Height() < int(buffer.ViewHeight()) {
		return
	}

	if uint(lines)+buffer.scrollLinesFromBottom >= (uint(buffer.Height()) - uint(buffer.ViewHeight())) {
		buffer.scrollLinesFromBottom = uint(buffer.Height()) - uint(buffer.ViewHeight())
	} else {
		buffer.scrollLinesFromBottom += uint(lines)
	}
}

func (buffer *Buffer) ScrollPageDown() {
	buffer.ScrollDown(buffer.viewHeight)
}
func (buffer *Buffer) ScrollPageUp() {
	buffer.ScrollUp(buffer.viewHeight)
}
func (buffer *Buffer) ScrollToEnd() {
	defer buffer.emitDisplayChange()
	buffer.scrollLinesFromBottom = 0
}

func (buffer *Buffer) SaveCursor() {
	buffer.savedX = buffer.cursorX
	buffer.savedY = buffer.cursorY
}

func (buffer *Buffer) RestoreCursor() {
	buffer.cursorX = buffer.savedX
	buffer.cursorY = buffer.savedY
}

func (buffer *Buffer) CursorAttr() *CellAttributes {
	return &buffer.cursorAttr
}

func (buffer *Buffer) GetCell(viewCol uint16, viewRow uint16) *Cell {
	rawLine := buffer.convertViewLineToRawLine(viewRow)
	return buffer.GetRawCell(viewCol, rawLine)
}

func (buffer *Buffer) GetRawCell(viewCol uint16, rawLine uint64) *Cell {

	if viewCol < 0 || rawLine < 0 || int(rawLine) >= len(buffer.lines) {
		return nil
	}
	line := &buffer.lines[rawLine]
	if int(viewCol) >= len(line.cells) {
		return nil
	}
	return &line.cells[viewCol]
}

func (buffer *Buffer) emitDisplayChange() {
	buffer.dirty = true
}

// Column returns cursor column
func (buffer *Buffer) CursorColumn() uint16 {
	// @todo originMode and left margin
	return buffer.cursorX
}

// Line returns cursor line
func (buffer *Buffer) CursorLine() uint16 {
	if buffer.originMode {
		result := buffer.cursorY - uint16(buffer.topMargin)
		if result < 0 {
			result = 0
		}
		return result
	}
	return buffer.cursorY
}

func (buffer *Buffer) TopMargin() uint {
	return buffer.topMargin
}

func (buffer *Buffer) BottomMargin() uint {
	return buffer.bottomMargin
}

// translates the cursor line to the raw buffer line
func (buffer *Buffer) RawLine() uint64 {
	return buffer.convertViewLineToRawLine(buffer.cursorY)
}

func (buffer *Buffer) convertViewLineToRawLine(viewLine uint16) uint64 {
	rawHeight := buffer.Height()
	if int(buffer.viewHeight) > rawHeight {
		return uint64(viewLine)
	}
	return uint64(int(viewLine) + (rawHeight - int(buffer.viewHeight)))
}

func (buffer *Buffer) convertRawLineToViewLine(rawLine uint64) uint16 {
	rawHeight := buffer.Height()
	if int(buffer.viewHeight) > rawHeight {
		return uint16(rawLine)
	}
	return uint16(int(rawLine) - (rawHeight - int(buffer.viewHeight)))
}

// Width returns the width of the buffer in columns
func (buffer *Buffer) Width() uint16 {
	return buffer.viewWidth
}

func (buffer *Buffer) ViewWidth() uint16 {
	return buffer.viewWidth
}

func (buffer *Buffer) Height() int {
	return len(buffer.lines)
}

func (buffer *Buffer) ViewHeight() uint16 {
	return buffer.viewHeight
}

func (buffer *Buffer) deleteLine() {
	index := int(buffer.RawLine())
	buffer.lines = buffer.lines[:index+copy(buffer.lines[index:], buffer.lines[index+1:])]
}

func (buffer *Buffer) insertLine() {

	defer buffer.emitDisplayChange()

	if !buffer.InScrollableRegion() {
		pos := buffer.RawLine()
		maxLines := buffer.getMaxLines()
		newLineCount := uint64(len(buffer.lines) + 1)
		if newLineCount > maxLines {
			newLineCount = maxLines
		}

		out := make([]Line, newLineCount)
		copy(
			out[:pos-(uint64(len(buffer.lines))+1-newLineCount)],
			buffer.lines[uint64(len(buffer.lines))+1-newLineCount:pos])
		out[pos] = newLine()
		copy(out[pos+1:], buffer.lines[pos:])
		buffer.lines = out
	} else {
		topIndex := buffer.convertViewLineToRawLine(uint16(buffer.topMargin))
		bottomIndex := buffer.convertViewLineToRawLine(uint16(buffer.bottomMargin))
		before := buffer.lines[:topIndex]
		after := buffer.lines[bottomIndex+1:]
		out := make([]Line, len(buffer.lines))
		copy(out[0:], before)

		pos := buffer.RawLine()
		for i := topIndex; i < bottomIndex; i++ {
			if i < pos {
				out[i] = buffer.lines[i]
			} else {
				out[i+1] = buffer.lines[i]
			}
		}

		copy(out[bottomIndex+1:], after)

		out[pos] = newLine()
		buffer.lines = out
	}
}

func (buffer *Buffer) InsertBlankCharacters(count int) {

	index := int(buffer.RawLine())
	for i := 0; i < count; i++ {
		cells := buffer.lines[index].cells
		buffer.lines[index].cells = append(cells[:buffer.cursorX], append([]Cell{buffer.defaultCell}, cells[buffer.cursorX:]...)...)
	}
}

func (buffer *Buffer) InsertLines(count int) {

	if buffer.HasScrollableRegion() && !buffer.InScrollableRegion() {
		// should have no effect outside of scrollable region
		return
	}

	buffer.cursorX = 0

	for i := 0; i < count; i++ {
		buffer.insertLine()
	}

}

func (buffer *Buffer) DeleteLines(count int) {

	if buffer.HasScrollableRegion() && !buffer.InScrollableRegion() {
		// should have no effect outside of scrollable region
		return
	}

	buffer.cursorX = 0

	for i := 0; i < count; i++ {
		buffer.deleteLine()
	}

}

func (buffer *Buffer) Index() {

	// This sequence causes the active position to move downward one line without changing the column position.
	// If the active position is at the bottom margin, a scroll up is performed."

	defer buffer.emitDisplayChange()

	if buffer.InScrollableRegion() {

		if uint(buffer.cursorY) < buffer.bottomMargin {
			buffer.cursorY++
		} else {

			topIndex := buffer.convertViewLineToRawLine(uint16(buffer.topMargin))
			bottomIndex := buffer.convertViewLineToRawLine(uint16(buffer.bottomMargin))

			for i := topIndex; i < bottomIndex; i++ {
				buffer.lines[i] = buffer.lines[i+1]
			}

			buffer.lines[bottomIndex] = newLine()
		}

		return
	}

	if buffer.cursorY >= buffer.ViewHeight()-1 {
		buffer.lines = append(buffer.lines, newLine())
		maxLines := buffer.getMaxLines()
		if uint64(len(buffer.lines)) > maxLines {
			copy(buffer.lines, buffer.lines[ uint64(len(buffer.lines)) - maxLines:])
			buffer.lines = buffer.lines[:maxLines]
		}
	} else {
		buffer.cursorY++
	}
}

func (buffer *Buffer) ReverseIndex() {

	defer buffer.emitDisplayChange()

	if buffer.InScrollableRegion() {

		if uint(buffer.cursorY) > buffer.topMargin {
			buffer.cursorY--
		} else {

			topIndex := buffer.convertViewLineToRawLine(uint16(buffer.topMargin))
			bottomIndex := buffer.convertViewLineToRawLine(uint16(buffer.bottomMargin))

			for i := bottomIndex; i > topIndex; i-- {
				buffer.lines[i] = buffer.lines[i-1]
			}

			buffer.lines[topIndex] = newLine()
		}
		return
	}

	if buffer.cursorY > 0 {
		buffer.cursorY--
	}
}

// Write will write a rune to the terminal at the position of the cursor, and increment the cursor position
func (buffer *Buffer) Write(runes ...rune) {

	// scroll to bottom on input
	buffer.scrollLinesFromBottom = 0

	for _, r := range runes {

		line := buffer.getCurrentLine()

		if buffer.replaceMode {

			if buffer.CursorColumn() >= buffer.Width() {
				// @todo replace rune at position 0 on next line down
				return
			}

			for int(buffer.CursorColumn()) >= len(line.cells) {
				line.cells = append(line.cells, buffer.defaultCell)
			}
			line.cells[buffer.cursorX].attr = buffer.cursorAttr
			line.cells[buffer.cursorX].setRune(r)
			buffer.incrementCursorPosition()
			continue
		}

		if buffer.CursorColumn() >= buffer.Width() { // if we're after the line, move to next

			if buffer.autoWrap {

				buffer.NewLineEx(true)

				newLine := buffer.getCurrentLine()
				if len(newLine.cells) == 0 {
					newLine.cells = append(newLine.cells, buffer.defaultCell)
				}
				cell := &newLine.cells[0]
				cell.setRune(r)
				cell.attr = buffer.cursorAttr

			} else {
				// no more room on line and wrapping is disabled
				return
			}

			// @todo if next line is wrapped then prepend to it and shuffle characters along line, wrapping to next if necessary
		} else {

			for int(buffer.CursorColumn()) >= len(line.cells) {
				line.cells = append(line.cells, buffer.defaultCell)
			}

			cell := &line.cells[buffer.CursorColumn()]
			cell.setRune(r)
			cell.attr = buffer.cursorAttr
		}

		buffer.incrementCursorPosition()
	}
}

func (buffer *Buffer) incrementCursorPosition() {
	// we can increment one column past the end of the line.
	// this is effectively the beginning of the next line, except when we \r etc.
	if buffer.CursorColumn() < buffer.Width() {
		buffer.cursorX++
	}
}

func (buffer *Buffer) inDoWrap() bool {
	// xterm uses 'do_wrap' flag for this special terminal state
	// we use the cursor position right after the boundary
	// let's see how it works out
	return buffer.cursorX == buffer.viewWidth // @todo rightMargin
}

func (buffer *Buffer) Backspace() {

	if buffer.cursorX == 0 {
		line := buffer.getCurrentLine()
		if line.wrapped {
			buffer.MovePosition(int16(buffer.Width()-1), -1)
		} else {
			//@todo ring bell or whatever - actually i think the pty will trigger this
		}
	} else if buffer.inDoWrap() {
		// the "do_wrap" implementation
		buffer.MovePosition(-2, 0)
	} else {
		buffer.MovePosition(-1, 0)
	}
}

func (buffer *Buffer) CarriageReturn() {

	for {
		line := buffer.getCurrentLine()
		if line == nil {
			break
		}
		if line.wrapped && buffer.cursorY > 0 {
			buffer.cursorY--
		} else {
			break
		}
	}

	buffer.cursorX = 0
}

func (buffer *Buffer) Tab() {
	tabSize := 4
	max := tabSize

	// @todo rightMargin
	if buffer.cursorX < buffer.viewWidth {
		max = int(buffer.viewWidth - buffer.cursorX - 1)
	}

	shift := tabSize - (int(buffer.cursorX+1) % tabSize)

	if shift > max {
		shift = max
	}

	for i := 0; i < shift; i++ {
		buffer.Write(' ')
	}
}

func (buffer *Buffer) NewLine() {
	buffer.NewLineEx(false)
}

func (buffer *Buffer) NewLineEx(forceCursorToMargin bool) {

	if buffer.IsNewLineMode() || forceCursorToMargin {
		buffer.cursorX = 0
	}
	buffer.Index()

	for {
		line := buffer.getCurrentLine()
		if !line.wrapped {
			break
		}
		buffer.Index()
	}
}

func (buffer *Buffer) SetNewLineMode() {
	buffer.lineFeedMode = false
}

func (buffer *Buffer) SetLineFeedMode() {
	buffer.lineFeedMode = true
}

func (buffer *Buffer) IsNewLineMode() bool {
	return buffer.lineFeedMode == false
}

func (buffer *Buffer) MovePosition(x int16, y int16) {

	var toX uint16
	var toY uint16

	if int16(buffer.CursorColumn())+x < 0 {
		toX = 0
	} else {
		toX = uint16(int16(buffer.CursorColumn()) + x)
	}

	// should either use CursorLine() and SetPosition() or use absolutes, mind Origin Mode (DECOM)
	if int16(buffer.CursorLine())+y < 0 {
		toY = 0
	} else {
		toY = uint16(int16(buffer.CursorLine()) + y)
	}

	buffer.SetPosition(toX, toY)
}

func (buffer *Buffer) SetPosition(col uint16, line uint16) {
	defer buffer.emitDisplayChange()

	useCol := col
	useLine := line
	maxLine := buffer.ViewHeight() - 1

	if buffer.originMode {
		useLine += uint16(buffer.topMargin)
		maxLine = uint16(buffer.bottomMargin)
		// @todo left and right margins
	}
	if useLine > maxLine {
		useLine = maxLine
	}

	if useCol >= buffer.ViewWidth() {
		useCol = buffer.ViewWidth() - 1
		//logrus.Errorf("Cannot set cursor position: column %d is outside of the current view width (%d columns)", col, buffer.ViewWidth())
	}

	buffer.cursorX = useCol
	buffer.cursorY = useLine
}

func (buffer *Buffer) GetVisibleLines() []Line {
	lines := []Line{}

	for i := buffer.Height() - int(buffer.ViewHeight()); i < buffer.Height(); i++ {
		y := i - int(buffer.scrollLinesFromBottom)
		if y >= 0 && y < len(buffer.lines) {
			lines = append(lines, buffer.lines[y])
		}
	}
	return lines
}

// tested to here

func (buffer *Buffer) Clear() {
	defer buffer.emitDisplayChange()
	for i := 0; i < int(buffer.ViewHeight()); i++ {
		buffer.lines = append(buffer.lines, newLine())
	}
	buffer.SetPosition(0, 0) // do we need to set position?
}

// creates if necessary
func (buffer *Buffer) getCurrentLine() *Line {
	return buffer.getViewLine(buffer.cursorY)
}

func (buffer *Buffer) getViewLine(index uint16) *Line {

	if index >= buffer.ViewHeight() { // @todo is this okay?#
		return &buffer.lines[len(buffer.lines)-1]
	}

	if len(buffer.lines) < int(buffer.ViewHeight()) {
		for int(index) >= len(buffer.lines) {
			buffer.lines = append(buffer.lines, newLine())
		}
		return &buffer.lines[int(index)]
	}

	if int(buffer.convertViewLineToRawLine(index)) < len(buffer.lines) {
		return &buffer.lines[buffer.convertViewLineToRawLine(index)]
	}

	panic(fmt.Sprintf("Failed to retrieve line for %d", index))
}

func (buffer *Buffer) EraseLine() {
	defer buffer.emitDisplayChange()
	line := buffer.getCurrentLine()
	line.cells = []Cell{}
}

func (buffer *Buffer) EraseLineToCursor() {
	defer buffer.emitDisplayChange()
	line := buffer.getCurrentLine()
	for i := 0; i <= int(buffer.cursorX); i++ {
		if i < len(line.cells) {
			line.cells[i].erase(buffer.defaultCell.attr.BgColour)
		}
	}
}

func (buffer *Buffer) EraseLineFromCursor() {
	defer buffer.emitDisplayChange()
	line := buffer.getCurrentLine()

	if len(line.cells) > 0 {
		cx := buffer.cursorX
		if int(cx) < len(line.cells) {
			line.cells = line.cells[:buffer.cursorX]
		}
	}

	max := int(buffer.ViewWidth()) - len(line.cells)

	buffer.SaveCursor()
	for i := 0; i < max; i++ {
		buffer.Write(0)
	}
	buffer.RestoreCursor()
}

func (buffer *Buffer) EraseDisplay() {
	defer buffer.emitDisplayChange()
	for i := uint16(0); i < (buffer.ViewHeight()); i++ {
		rawLine := buffer.convertViewLineToRawLine(i)
		if int(rawLine) < len(buffer.lines) {
			buffer.lines[int(rawLine)].cells = []Cell{}
		}
	}
}

func (buffer *Buffer) DeleteChars(n int) {
	defer buffer.emitDisplayChange()

	line := buffer.getCurrentLine()
	if int(buffer.cursorX) >= len(line.cells) {
		return
	}
	before := line.cells[:buffer.cursorX]
	if int(buffer.cursorX)+n >= len(line.cells) {
		n = len(line.cells) - int(buffer.cursorX)
	}
	after := line.cells[int(buffer.cursorX)+n:]
	line.cells = append(before, after...)
}

func (buffer *Buffer) EraseCharacters(n int) {
	defer buffer.emitDisplayChange()

	line := buffer.getCurrentLine()

	max := int(buffer.cursorX) + n
	if max > len(line.cells) {
		max = len(line.cells)
	}

	for i := int(buffer.cursorX); i < max; i++ {
		line.cells[i].erase(buffer.defaultCell.attr.BgColour)
	}
}

func (buffer *Buffer) EraseDisplayFromCursor() {
	defer buffer.emitDisplayChange()
	line := buffer.getCurrentLine()

	max := int(buffer.cursorX)
	if max > len(line.cells) {
		max = len(line.cells)
	}

	line.cells = line.cells[:max]
	for i := buffer.cursorY + 1; i < buffer.ViewHeight(); i++ {
		rawLine := buffer.convertViewLineToRawLine(i)
		if int(rawLine) < len(buffer.lines) {
			buffer.lines[int(rawLine)].cells = []Cell{}
		}
	}
}

func (buffer *Buffer) EraseDisplayToCursor() {
	defer buffer.emitDisplayChange()
	line := buffer.getCurrentLine()

	for i := 0; i <= int(buffer.cursorX); i++ {
		if i >= len(line.cells) {
			break
		}
		line.cells[i].erase(buffer.defaultCell.attr.BgColour)
	}
	for i := uint16(0); i < buffer.cursorY; i++ {
		rawLine := buffer.convertViewLineToRawLine(i)
		if int(rawLine) < len(buffer.lines) {
			buffer.lines[int(rawLine)].cells = []Cell{}
		}
	}
}

func (buffer *Buffer) ResizeView(width uint16, height uint16) {

	defer buffer.emitDisplayChange()

	if buffer.viewHeight == 0 {
		buffer.viewWidth = width
		buffer.viewHeight = height
		return
	}

	// @todo scroll to bottom on resize
	line := buffer.getCurrentLine()
	cXFromEndOfLine := len(line.cells) - int(buffer.cursorX+1)

	cursorYMovement := 0

	if width < buffer.viewWidth { // wrap lines if we're shrinking
		for i := 0; i < len(buffer.lines); i++ {
			line := &buffer.lines[i]
			//line.Cleanse()
			if len(line.cells) > int(width) { // only try wrapping a line if it's too long
				sillyCells := line.cells[width:] // grab the cells we need to wrap
				line.cells = line.cells[:width]

				// we need to move cut cells to the next line
				// if the next line is wrapped anyway, we can push them onto the beginning of that line
				// otherwise, we need add a new wrapped line
				if i+1 < len(buffer.lines) {
					nextLine := &buffer.lines[i+1]
					if nextLine.wrapped {

						nextLine.cells = append(sillyCells, nextLine.cells...)
						continue
					}
				}

				if i+1 <= int(buffer.cursorY) {
					cursorYMovement++
				}

				newLine := newLine()
				newLine.setWrapped(true)
				newLine.cells = sillyCells
				after := append([]Line{newLine}, buffer.lines[i+1:]...)
				buffer.lines = append(buffer.lines[:i+1], after...)

			}
		}
	} else if width > buffer.viewWidth { // unwrap lines if we're growing
		for i := 0; i < len(buffer.lines)-1; i++ {
			line := &buffer.lines[i]
			//line.Cleanse()
			for offset := 1; i+offset < len(buffer.lines); offset++ {
				nextLine := &buffer.lines[i+offset]
				//nextLine.Cleanse()
				if !nextLine.wrapped { // if the next line wasn't wrapped, we don't need to move characters back to this line
					break
				}
				spaceOnLine := int(width) - len(line.cells)
				if spaceOnLine <= 0 { // no more space to unwrap
					break
				}
				moveCount := spaceOnLine
				if moveCount > len(nextLine.cells) {
					moveCount = len(nextLine.cells)
				}
				line.cells = append(line.cells, nextLine.cells[:moveCount]...)
				if moveCount == len(nextLine.cells) {

					if i+offset <= int(buffer.cursorY) {
						cursorYMovement--
					}

					// if we unwrapped all cells off the next line, delete it
					buffer.lines = append(buffer.lines[:i+offset], buffer.lines[i+offset+1:]...)

					offset--

				} else {
					// otherwise just remove the characters we moved up a line
					nextLine.cells = nextLine.cells[moveCount:]
				}
			}

		}
	}

	buffer.viewWidth = width
	buffer.viewHeight = height

	cY := uint16(len(buffer.lines) - 1)
	if cY >= buffer.viewHeight {
		cY = buffer.viewHeight - 1
	}
	buffer.cursorY = cY

	// position cursorX
	line = buffer.getCurrentLine()
	buffer.cursorX = uint16((len(line.cells) - cXFromEndOfLine) - 1)

	buffer.ResetVerticalMargins()
}

func (buffer *Buffer) getMaxLines() uint64 {
	result := buffer.maxLines
	if result < uint64(buffer.viewHeight) {
		result = uint64(buffer.viewHeight)
	}

	return result
}

func (buffer *Buffer) Save(path string) {
	f, err := os.Create(path)
	if err != nil {
		panic(err)
	}
	defer f.Close()

	for _, line := range buffer.lines {
		f.WriteString(line.String())
	}
}

func (buffer *Buffer) Compare(path string) bool {
	f, err := ioutil.ReadFile(path)
	if err != nil {
		panic(err)
	}

	bufferContent := []byte{}
	for _, line := range buffer.lines {
		lineBytes := []byte(line.String())
		bufferContent = append(bufferContent, lineBytes...)
	}
	return bytes.Equal(f, bufferContent)
}

