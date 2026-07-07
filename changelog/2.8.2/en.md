## Fixes
- Log viewer: copying a large selection now puts the WHOLE selected range on the clipboard — previously Ctrl+C / context-menu Copy after a long drag only captured the lines currently on screen.
- Log viewer: dragging a selection past the end of a line, over blank lines, or across the viewport edge no longer makes it jump to the top of the log (desktop).
- Log viewer: a drag selection stays inside the log content — the toolbar and window chrome are no longer pulled into it.
- Log viewer: after copying via right-click, the selection no longer follows the mouse cursor, and wheel scrolling no longer becomes slow while a selection is active.
- Log viewer: scrolling up through a large log no longer stutters partway up.
