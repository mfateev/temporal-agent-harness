# Codex UI Implementation Research

## Overview

Codex uses a sophisticated TUI (Terminal User Interface) implementation based on **ratatui** (terminal UI framework) and **crossterm** (terminal manipulation library). This document summarizes their approach to cursor handling, rendering, and terminal management.

## Architecture

### Core Components

1. **ratatui** - Modern Rust TUI framework
   - Widget-based rendering system
   - Double-buffered rendering (compares previous and current frames)
   - Efficient diff-based updates (only renders changes)

2. **crossterm** - Cross-platform terminal manipulation
   - Cursor positioning (`MoveTo`)
   - Terminal clearing
   - Color and style management
   - Raw mode and event handling

3. **Custom Terminal** (`custom_terminal.rs`)
   - Custom fork of ratatui's Terminal
   - Enhanced cursor position tracking
   - Optimized rendering with `ClearToEnd` commands

## Key Concepts

### 1. Frame-Based Rendering

```rust
pub struct Frame<'a> {
    pub(crate) cursor_position: Option<Position>,
    pub(crate) viewport_area: Rect,
    pub(crate) buffer: &'a mut Buffer,
}
```

**How it works:**
- Each frame has an optional cursor position
- If `None`, cursor is hidden
- If `Some((x, y))`, cursor is shown at that position after draw
- Widgets can set cursor position via `frame.set_cursor_position((x, y))`

**Usage in Codex:**
```rust
// From app.rs line 1291-1292
if let Some((x, y)) = self.chat_widget.cursor_pos(frame.area()) {
    frame.set_cursor_position((x, y));
}
```

### 2. Double Buffering

Codex uses two buffers to track changes:

```rust
buffers: [Buffer; 2],
current: usize,  // Index of current buffer
```

**Rendering process:**
1. Render widgets to current buffer
2. Compare current buffer with previous buffer (diff)
3. Generate draw commands for only changed cells
4. Swap buffers for next frame

**Benefits:**
- Only updates changed parts of the terminal
- Reduces flickering
- Improves performance (especially over SSH/remote)

### 3. Efficient Diff Algorithm

```rust
fn diff_buffers(a: &Buffer, b: &Buffer) -> Vec<DrawCommand>
```

**Optimizations:**
- `ClearToEnd` command: Instead of printing spaces to clear a line, use terminal's "clear to end of line" escape sequence
- Multi-width character handling: Properly tracks Chinese/Japanese/emoji that take 2+ columns
- Selective updates: Only sends changed cells, skips unchanged regions

**Draw commands:**
```rust
enum DrawCommand {
    Put { x: u16, y: u16, cell: Cell },
    ClearToEnd { x: u16, y: u16, bg: Color },
}
```

### 4. Cursor Position Tracking

```rust
pub last_known_cursor_pos: Position,
```

**Why track cursor:**
- Supports viewport resizing (terminal grows/shrinks)
- Enables inline rendering (cursor stays where LLM is typing)
- Allows scrollback preservation

**Cursor flow:**
1. Get initial cursor position from backend
2. Track cursor after each frame render
3. Restore cursor position after drawing
4. Update last known position for next frame

### 5. Viewport Management

```rust
pub viewport_area: Rect,
```

**Flexible viewport:**
- Not always full screen
- Can be inline (starts at current cursor, grows downward)
- Supports partial terminal usage
- Allows running alongside other terminal output

### 6. Terminal Control Flow

```rust
pub fn try_draw<F, E>(&mut self, render_callback: F) -> io::Result<()>
where
    F: FnOnce(&mut Frame) -> Result<(), E>,
{
    self.autoresize()?;              // 1. Check if terminal resized
    let mut frame = self.get_frame(); // 2. Get frame with buffer
    render_callback(&mut frame)?;     // 3. Render widgets
    let cursor_position = frame.cursor_position; // 4. Extract cursor
    self.flush()?;                    // 5. Write diff to terminal

    // 6. Set cursor visibility and position
    match cursor_position {
        None => self.hide_cursor()?,
        Some(position) => {
            self.show_cursor()?;
            self.set_cursor_position(position)?;
        }
    }

    self.swap_buffers();             // 7. Swap for next frame
    Backend::flush(&mut self.backend)?; // 8. Flush terminal
    Ok(())
}
```

## Codex Project Structure

```
codex-rs/tui/src/
├── app.rs                      # Main TUI app (132KB!)
├── custom_terminal.rs          # Custom terminal with cursor tracking
├── chatwidget.rs              # Main chat interface (281KB!)
├── markdown_render.rs         # Markdown rendering
├── history_cell.rs            # Individual message cells
├── streaming/                 # Streaming response handling
├── bottom_pane/              # Input area
└── render/                   # Rendering utilities
```

## Key Implementation Details

### 1. Cursor Position in Chat Widget

Chat widgets calculate their own cursor positions:

```rust
fn cursor_pos(&self, area: Rect) -> Option<(u16, u16)>
```

- Each widget knows where its cursor should be
- Returns `None` if widget doesn't need cursor
- Returns `Some((x, y))` for editable widgets (like input field)

### 2. Rendering Loop

```rust
terminal.draw(|frame| {
    app.render(frame);
})?;
```

- Called in main loop
- App renders all widgets to frame
- Terminal handles diff and cursor automatically

### 3. Crossterm Integration

```rust
use crossterm::cursor::MoveTo;
use crossterm::queue;
use crossterm::style::Print;
use crossterm::terminal::Clear;
```

**Used for:**
- Moving cursor: `queue!(writer, MoveTo(x, y))?`
- Printing text: `queue!(writer, Print(cell.symbol()))?`
- Clearing regions: `queue!(writer, Clear(ClearType::UntilNewLine))?`
- Batching commands with `queue!` then `flush`

### 4. Color and Style Management

```rust
if cell.fg != fg || cell.bg != bg {
    queue!(writer, SetColors(Colors::new(cell.fg.into(), cell.bg.into())))?;
}
```

- Tracks current fg/bg colors
- Only sends color change commands when colors actually change
- Reduces escape sequences sent to terminal

## Comparison to Current codex-temporal-go

### Current Implementation (Go)

```go
// internal/cli/renderer.go
type Renderer struct {
    markdown goldmark.Markdown
}

func (r *Renderer) RenderMarkdown(content string) {
    var buf bytes.Buffer
    r.markdown.Convert([]byte(content), &buf)
    fmt.Print(buf.String())
}
```

**Limitations:**
- No cursor management
- No double buffering
- Redraws entire output every time
- No efficient diff-based updates
- Cannot show cursor in input while streaming

### Codex's Approach (Rust)

- Double-buffered rendering
- Cursor positioned at input field
- Efficient diffs (only updates changed cells)
- Supports streaming while showing cursor
- Handles terminal resize gracefully

## Recommendations for codex-temporal-go

### Option 1: Keep Current Simple Approach

**Pros:**
- Simple to understand
- No complex dependencies
- Works for basic use cases

**Cons:**
- Cannot show cursor during streaming
- Flickering on updates
- Inefficient over slow connections
- No proper input field UI

### Option 2: Use Go TUI Library

**Available options:**
- **tview** - High-level TUI framework (similar to ratatui)
- **tcell** - Terminal handling (similar to crossterm)
- **bubbletea** - Elm-inspired TUI framework (modern, popular)
- **termui** - Dashboard-style TUI

**Recommended: bubbletea + lipgloss**
```go
import (
    tea "github.com/charmbracelet/bubbletea"
    "github.com/charmbracelet/lipgloss"
)
```

Benefits:
- Modern Go idioms
- Good documentation
- Active community
- Similar concepts to ratatui (components, messages, updates)

### Option 3: Minimal Cursor Management

Add basic cursor control without full TUI framework:

```go
import "github.com/mattn/go-isatty"

// Save cursor position
fmt.Print("\033[s")

// Restore cursor position
fmt.Print("\033[u")

// Move cursor to position
fmt.Printf("\033[%d;%dH", row, col)

// Clear from cursor to end of line
fmt.Print("\033[K")
```

**Pros:**
- Minimal dependencies
- Just enough for basic cursor control
- Can show input cursor while streaming

**Cons:**
- Still inefficient (no diffing)
- Manual escape sequence management
- Cross-platform quirks

## Key Takeaways

1. **Cursor Position is Explicit**: Widgets explicitly tell the frame where the cursor should be
2. **Double Buffering is Essential**: Prevents flickering and improves performance
3. **Diff-Based Rendering**: Only update changed cells for efficiency
4. **ClearToEnd Optimization**: Use terminal's built-in clear commands instead of printing spaces
5. **Viewport Flexibility**: Don't assume full-screen, support inline rendering
6. **Multi-Width Characters**: Properly handle Unicode width (Chinese, emoji, etc.)

## Next Steps for codex-temporal-go

1. **Decide on approach**: Simple (current), Full TUI (bubbletea), or Minimal (escape sequences)
2. **If using TUI**: Evaluate bubbletea vs tview vs tcell
3. **Start with cursor control**: Show cursor in input field while streaming responses
4. **Add double buffering**: Prevent flickering during updates
5. **Implement diff rendering**: Only update changed parts (can be gradual)

## References

- Codex TUI: `/home/sprite/workarea/repos/codex/codex-rs/tui/`
- ratatui docs: https://ratatui.rs/
- crossterm docs: https://docs.rs/crossterm/
- bubbletea: https://github.com/charmbracelet/bubbletea
- ANSI escape codes: https://en.wikipedia.org/wiki/ANSI_escape_code
