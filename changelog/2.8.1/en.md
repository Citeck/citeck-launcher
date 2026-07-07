## Fixes
- Log viewer: the view no longer jumps while you are scrolled up reading — incoming lines stop shifting the content until you return to the live tail.
- Log viewer: sticking to the newest lines now releases reliably with a single upward scroll, even on very busy logs, and re-engages at the bottom or via the Follow button.
- Log viewer: selecting text works while the log is streaming — large selections are no longer dropped by incoming lines, and Ctrl+A no longer slows scrolling down afterwards.
- Log viewer: lines written at the exact moment the viewer opens are no longer lost.

## Changes
- Log viewer: smoother rendering on very chatty logs — updates are applied in small batches.
