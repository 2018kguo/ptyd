# Conformance test sources

Test data and cases here come from other terminal emulators' test suites.
They are used under their licenses; each source keeps its license file.

| Source | License | Revision | How it is used |
|---|---|---|---|
| [libvterm](https://github.com/neovim/libvterm) `t/*.test` | MIT (`libvterm/LICENSE`) | 934bc2f | Scripts vendored unchanged in `libvterm/`, run by `libvterm_test.go`. |
| [alacritty](https://github.com/alacritty/alacritty) `alacritty_terminal/tests/ref` | Apache-2.0 or MIT (`alacritty/LICENSE-*`) | 5421470 | Recordings gzipped; each `grid.json` converted by `alacritty/alacritty_conv.py` to `expected.json.gz` (per-line text, styles, wrap flag). Run by `alacritty_test.go`. |
| [ghostty](https://github.com/ghostty-org/ghostty) `src/terminal/Terminal.zig`, `stream_terminal.zig` unit tests | MIT (`LICENSE.ghostty`) | 521cf2c | Translated into Go tables (`conformance_ghostty*_test.go`). |
| [wezterm](https://github.com/wezterm/wezterm) `term/src/test` | MIT (`LICENSE.wezterm`) | e2a98e1 | Translated into Go tables (`conformance_wezterm_test.go`). |
| [esctest2](https://github.com/ThomasDickey/esctest2) | GPL-2.0 | 2798f12 | Not vendored and nothing copied: `conformance_esctest_test.go` holds independently written cases for the xterm behaviors esctest2 checks. |

Copyright notices: libvterm, Copyright (c) 2008 Paul Evans; alacritty, The Alacritty Project contributors;
ghostty, Copyright (c) 2024
Mitchell Hashimoto, Ghostty contributors; wezterm, Copyright (c)
2018-Present Wez Furlong.
