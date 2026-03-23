# Windows Compatibility Analysis: Porting Claude Squad to Zellij on Windows

## Executive Summary

Porting Claude Squad to Windows via Zellij would be a **major undertaking** due to two
compounding factors: (1) Zellij itself lacks stable Windows support, and (2) the codebase
has deep, unabstracted tmux coupling throughout.

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

## Conclusion

The most pragmatic path for Windows users today is **WSL + tmux**. For long-term Windows
support, creating a multiplexer abstraction interface is the right first step — it
decouples the architecture from tmux and allows adding Zellij (or ConPTY-based) backends
when they mature. Targeting Zellij on Windows specifically is premature given Zellij's own
Windows support is still being merged.
