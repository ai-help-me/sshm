
# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

---

## Project Overview

**sshm** is a professional terminal SSH client that provides:

- Interactive TUI host selection
- Interactive SSH shell sessions (true terminal behavior)
- Interactive SFTP file transfer shell
- Jump host and callback shell support

⚠️ This project is **not** a simple SSH wrapper.  
It behaves like a real SSH terminal client (similar to OpenSSH / iTerm / SecureCRT).

Because of this, **terminal (TTY) lifecycle management and SFTP path semantics are first-class architectural concerns**.

sshw write in go , it read config format :
```yaml
- name: {name}
  host: {host}
  user: {username}
  port: {post}
  password:  {password}
- name: k3s
  children:
    - name: 192.168.1.16
      host: 192.168.1.16
      user: root
      password: {password}
```
---

## ⚠️ CRITICAL: Terminal (TTY) Lifecycle Rules

This project must strictly follow correct Unix terminal semantics.

### There are ONLY two terminal modes in this program

| Mode | Used For | Behavior |
|---|---|---|
| **Cooked mode (normal TTY)** | TUI, menus, SFTP shell, prompts | Ctrl+C is SIGINT, line editing works |
| **Raw mode** | ONLY during SSH interactive shell | All keystrokes forwarded to remote PTY |

> Raw mode is **temporary** and must exist in a very small, well-defined scope.

---

## 🚫 Absolute Prohibitions

AI and developers must **NEVER**:

- Call `term.MakeRaw` anywhere except in `terminal.Manager`
- Handle Ctrl+C manually inside SSH/SFTP logic
- Flush stdin
- Use `unix.Poll` to protect stdin behavior
- Mix terminal logic into client/session code
- Treat raw mode as session state

If any of these appear, the design is wrong.

---

## ✅ Required Architecture: Terminal Manager Layer

A dedicated package must exist:

```
pkg/terminal/manager.go
```

This is the **ONLY** place allowed to:

- Call `term.MakeRaw`
- Call `term.Restore`
- Handle SIGWINCH (window resize)
- Track whether we are in raw mode

### Terminal Manager API

```go
EnterRaw(session *ssh.Session)
Restore()
InRaw() bool
```

Responsibilities:

1. Save original TTY state
2. Enter raw mode
3. Listen for SIGWINCH and call `session.WindowChange`
4. Guarantee restore on exit (even on panic)

---

## ⚠️ CRITICAL: SFTP Path Management Rules

SFTP protocol has NO current working directory.

SFTPShell must simulate TWO independent CWD systems:

- LocalCWD  (lcd / lls / lget / lput)
- RemoteCWD (cd / ls / get / put)

### Mandatory state

```go
type PathState struct {
    LocalCWD   string
    RemoteCWD  string
    HomeLocal  string
    HomeRemote string
}
```

### Every command must resolve paths

Never trust user input directly.

Use:

```go
ResolveLocal(path string)
ResolveRemote(path string)
```

### After every successful cd, MUST:

```go
real, _ := sftpClient.RealPath(resolved)
RemoteCWD = real
```

This prevents path drifting due to symlinks and `..` handling.

### Command rules

| Command | Remote uses | Local uses |
|---|---|---|
| get | RemoteCWD | LocalCWD |
| put | LocalCWD | RemoteCWD |
| ls  | RemoteCWD | — |
| lls | LocalCWD  | — |

---

## Mental Model

sshm is:

> Terminal Emulator → SSH/SFTP are plugins

NOT:

> SSH tool with terminal and path hacks

