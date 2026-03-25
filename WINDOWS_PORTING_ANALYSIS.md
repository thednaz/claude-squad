# Windows Compatibility Analysis: Porting Claude Squad to Windows

## Executive Summary

Two multiplexer backends were evaluated for Windows support:

- **Zellij**: Major undertaking (~2-4 weeks). Zellij lacks stable Windows support and uses
  a completely different API, requiring a full multiplexer abstraction layer.
- **psmux** (Recommended): Small-to-medium effort (~3-5 days). psmux is a native Windows
  tmux implementation in Rust that speaks the same tmux command language. Claude Squad's
  `exec.Command("tmux", ...)` calls work as-is through psmux's `tmux` alias.

## Current Platform Dependencies

### Tmux Integration (No Abstraction Layer)

The codebase directly invokes tmux commands via `exec.Command("tmux", ...)` with no
multiplexer interface or abstraction. All tmux logic lives in `session/tmux/`:

| File | Lines | Responsibility |
|------|-------|----------------|
| `tmux.go` | 516 | Core session lifecycle, pane capture, key sending |
| `tmux_unix.go` | 79 | SIGWINCH-based window resize monitoring |
| `tmux_windows.go` | 58 | Polling-based window resize (250ms ticker) |
| `pty.go` | 27 | PTY abstraction wrapping `creack/pty` |

**Tmux commands used:**
- `tmux new-session -d -s <name>` — create detached sessions
- `tmux attach-session -t <name>` — attach to sessions
- `tmux kill-session -t <name>` — terminate sessions
- `tmux capture-pane -p -e -J` — capture pane content with ANSI colors
- `tmux set-option -t <name> history-limit/mouse` — configure sessions
- `tmux has-session -t=<name>` — check session existence
- `tmux send-keys` — inject input
- `tmux ls` — list sessions for cleanup

### Unix-Specific Dependencies

| Dependency | Package | Usage |
|------------|---------|-------|
| PTY | `github.com/creack/pty` v1.1.24 | Start commands in pseudo-terminal |
| Signals | `os/signal`, `syscall.SIGWINCH` | Window resize detection (Unix only) |
| Process groups | `syscall.SysProcAttr{Setsid: true}` | Daemon process detachment |
| Terminal size | `golang.org/x/term` | Query terminal dimensions |

### Already Platform-Aware Code

The codebase already has some Windows awareness via build tags:
- `daemon/daemon_unix.go` / `daemon/daemon_windows.go` — process detachment
- `session/tmux/tmux_unix.go` / `tmux_windows.go` — window resize monitoring

## Zellij on Windows: Current State (March 2026)

Zellij does **not** have stable Windows support. Key facts:
- Windows support has been requested since 2021 (zellij-org/zellij#316)
- A Windows support branch is actively being merged (zellij-org/zellij#4745, Feb 2026)
- Contributors report it "is starting to work really well" with Alacritty
- **Not production-ready** — still tracking bugs and implementation issues

## What a Port Would Require

### 1. Create Multiplexer Interface (~1-2 days)

Extract a `Multiplexer` interface from `TmuxSession`:

```go
type Multiplexer interface {
    Start(workDir string) error
    Restore() error
    Attach() (chan struct{}, error)
    Detach()
    Close() error
    SendKeys(keys string) error
    CapturePaneContent() (string, error)
    SetDetachedSize(width, height int) error
    DoesSessionExist() bool
    HasUpdated() (bool, bool)
}
```

Refactor `instance.go` to depend on interface instead of `*TmuxSession`.

### 2. Implement Zellij Backend (~3-5 days)

Zellij has a fundamentally different model:
- **Sessions vs Layouts**: Zellij uses layout files, not ad-hoc session creation
- **CLI differences**: `zellij run`, `zellij action write-chars`, `zellij action dump-screen`
- **Pane capture**: No direct equivalent to `tmux capture-pane -p -e -J`
- **IPC**: Pipe-based communication model vs tmux's command-based approach

### 3. Replace PTY Library (~2-3 days)

`creack/pty` is Unix-only. Options:
- Windows ConPTY via `golang.org/x/sys/windows`
- Cross-platform wrapper (e.g., `github.com/aymanbagabas/go-pty`)
- Custom ConPTY integration

### 4. Windows Platform Support (~2-3 days)

- Shell detection (`$SHELL` doesn't exist; need PowerShell/cmd detection)
- Path handling (backslashes, drive letters in git worktree paths)
- Process management (already partially done in `daemon_windows.go`)
- Installation (current `install.sh` is bash-only)

### 5. Testing (~3-5 days)

- No existing Windows CI infrastructure
- Edge cases: ConPTY, Windows Terminal, PowerShell vs cmd vs Git Bash
- Git worktree operations on NTFS

## Total Estimated Effort

**~2-4 weeks** for an experienced Go developer, assuming Zellij Windows stabilizes.

## Recommended Alternatives

| Approach | Effort | Trade-off |
|----------|--------|-----------|
| **WSL** (use tmux in WSL as-is) | None | Requires WSL installation |
| **Multiplexer abstraction** (interface + tmux + future Zellij) | Medium | Good architecture, Zellij can be added when ready |
| **ConPTY native** (no multiplexer, direct Windows terminal) | Medium | No external dependency, but reimplements multiplexer features |
| **Full Zellij Windows port** | Large | Targets an unstable platform |

## psmux: The Better Path (Updated Analysis)

### What is psmux?

[psmux](https://github.com/psmux/psmux) is a native Windows terminal multiplexer built
from scratch in Rust. It uses Windows ConPTY directly and speaks the **tmux command
language** — 76 commands, 126+ format variables, `.tmux.conf` compatibility. It ships
`tmux` and `pmux` aliases, so `exec.Command("tmux", ...)` works without code changes.

Install via Scoop, Chocolatey, or `cargo install psmux`.

### Command Compatibility with Claude Squad

| Claude Squad Usage | tmux Command | psmux Support |
|---|---|---|
| Create session | `new-session -d -s <name> -c <dir>` | Yes |
| Attach | `attach-session -t <name>` | Yes |
| Kill session | `kill-session -t <name>` | Yes |
| Capture output | `capture-pane -p -e -J -t <name>` | Yes |
| Capture range | `capture-pane -p -e -J -S <start> -E <end>` | Yes |
| Send input | `send-keys -t <name> <keys>` | Yes |
| Set options | `set-option -t <name> history-limit/mouse` | Yes |
| Check existence | `has-session -t=<name>` | Yes |
| List sessions | `ls` | Yes |
| Session cleanup | `kill-session -t <match>` | Yes |

**All 10 tmux commands used by claude-squad are supported by psmux.**

### What Needs to Change (psmux approach)

#### 1. Replace `creack/pty` on Windows (~1-2 days)

The primary blocker. `creack/pty` is Unix-only. The attach flow in `tmux.go` uses raw
PTY for stdin/stdout forwarding. Options:
- Use `github.com/aymanbagabas/go-pty` (cross-platform, supports ConPTY)
- Build-tag split: `pty_unix.go` keeps `creack/pty`, `pty_windows.go` uses ConPTY
- Refactor attach to use psmux's built-in attach (eliminates PTY dependency)

#### 2. Platform Fixes (~1 day)

- Shell detection: `$SHELL` doesn't exist on Windows; detect PowerShell/cmd
- Config path: `os.UserHomeDir()` already works (`C:\Users\<name>`)
- Git paths: `go-git` handles path separators, but verify worktree paths
- Install script: Add PowerShell install script or Scoop manifest

#### 3. Already Done

- Signal handling: `tmux_windows.go` already polls instead of SIGWINCH
- Daemon process: `daemon_windows.go` already uses `CREATE_NEW_PROCESS_GROUP`
- TUI: BubbleTea is cross-platform
- Git: go-git is cross-platform

#### 4. Testing (~1-2 days)

- Verify `capture-pane -p -e -J` output format matches tmux exactly
- Test ANSI color handling in Windows Terminal
- Validate session lifecycle with psmux
- Test git worktree operations on NTFS

### Estimated Effort: ~3-5 days

| Task | Effort |
|------|--------|
| PTY replacement (build-tagged) | 1-2 days |
| Platform fixes (shell, paths, install) | 1 day |
| Testing & edge cases | 1-2 days |
| **Total** | **3-5 days** |

## Comparison of Approaches

| Approach | Effort | Notes |
|----------|--------|-------|
| **psmux on Windows** | **~3-5 days** | Same tmux commands, native Windows, recommended |
| WSL + tmux | Zero | Requires WSL installation |
| Zellij on Windows | ~2-4 weeks | Different API, unstable Windows support |
| ConPTY native (no multiplexer) | ~2 weeks | Reimplements multiplexer features |

## Conclusion

**psmux is the recommended path for Windows support.** Its tmux command compatibility
means claude-squad's core logic works unchanged — the `tmux` binary on the PATH just
happens to be psmux instead. The only significant work is replacing the Unix PTY layer
with a Windows ConPTY equivalent, plus minor platform polish. This is a ~3-5 day effort
versus ~2-4 weeks for a Zellij-based approach.
