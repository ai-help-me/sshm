# SSHM - 专业终端 SSH 客户端

[![Go Version](https://img.shields.io/badge/Go-1.24+-blue.svg)](https://golang.org)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

SSHM 是一个专业的终端 SSH 客户端，提供交互式 TUI 主机选择、真正的 SSH 终端会话以及交互式 SFTP 文件传输功能。


[![asciicast](https://asciinema.org/a/kQKetwXrgvtSV7xA.svg)](https://asciinema.org/a/kQKetwXrgvtSV7xA)

## 功能特性

### 交互式 TUI 界面
- 美观的终端用户界面，支持键盘导航
- 支持主机分组管理，便于组织大量服务器
- 实时搜索过滤功能，快速定位目标主机
- 面包屑导航，清晰展示当前路径

### SSH 会话
- 真正的 SSH 终端行为（类似 OpenSSH / iTerm / SecureCRT）
- 支持密码认证和 SSH 密钥认证
- 完整的终端生命周期管理

### SFTP 文件传输
- 交互式 SFTP Shell，类似传统 FTP 客户端
- 支持上传（put）和下载（get）文件
- 本地和远程目录独立管理
- 实时进度条显示传输进度
- 支持 Ctrl+C 中断传输

### 配置文件
- 简洁的 YAML 配置格式
- 支持主机分组和嵌套
- 兼容 sshw 配置文件格式

## 安装
```bash
go install github.com/ai-help-me/sshm@latest
```
### 从源码编译

```bash
# 克隆仓库
git clone https://github.com/ai-help-me/sshm.git
cd sshm

# 编译
go build -o sshm

# 安装到系统（可选）
go install
```

### 依赖要求

- Go 1.24 或更高版本

## 使用方法

### 1. 创建配置文件

在 home 目录创建 `~/.sshm.yaml` 配置文件：

```yaml
# 简单主机配置
- name: web-server
  host: 192.168.1.10
  user: root
  port: 22
  password: your-password

# 使用密钥认证
- name: db-server
  host: db.example.com
  user: ubuntu
  port: 22
  keypath: ~/.ssh/id_rsa

# 主机分组
- name: k8s-cluster
  children:
    - name: master
      host: 192.168.1.20
      user: root
      password: password123
    - name: worker1
      host: 192.168.1.21
      user: root
      password: password123
    - name: worker2
      host: 192.168.1.22
      user: root
      password: password123
```

### 2. 启动程序

```bash
sshm
```

### 3. TUI 操作指南

| 按键 | 功能 |
|------|------|
| `↑` / `↓` 或 `k` / `j` | 上下移动选择 |
| `Enter` | 选择主机或进入分组 |
| `Esc` | 返回上一级 |
| `/` | 进入搜索模式 |
| `q` / `Ctrl+C` | 退出程序 |

选择主机后，会提示选择连接方式：
- **SSH**: 进入交互式 SSH 终端
- **SFTP**: 进入 SFTP 文件传输 Shell

## SFTP Shell 命令

进入 SFTP 模式后，可以使用以下命令：

### 目录操作
| 命令 | 说明 | 示例 |
|------|------|------|
| `cd [path]` | 切换远程目录 | `cd /var/log` |
| `lcd [path]` | 切换本地目录 | `lcd ~/Downloads` |
| `pwd` | 显示远程当前目录 | `pwd` |
| `lpwd` | 显示本地当前目录 | `lpwd` |

### 文件列表
| 命令 | 说明 | 示例 |
|------|------|------|
| `ls [path]` | 列出远程文件 | `ls /tmp` |
| `lls [path]` | 列出本地文件 | `lls .` |

### 文件传输
| 命令 | 说明 | 示例 |
|------|------|------|
| `get <remote> [local]` | 下载文件 | `get file.txt` 或 `get /remote/file.txt ~/local/file.txt` |
| `put <local> [remote]` | 上传文件 | `put file.txt` 或 `put ~/local/file.txt /remote/file.txt` |

### 其他命令
| 命令 | 说明 |
|------|------|
| `help` 或 `?` | 显示帮助信息 |
| `exit` / `quit` / `bye` | 退出 SFTP Shell |

## 配置说明

### 主机配置项

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `name` | string | 是 | 主机显示名称 |
| `host` | string | 是* | 主机地址（IP 或域名） |
| `user` | string | 是* | 登录用户名 |
| `port` | int | 否 | SSH 端口，默认 22 |
| `password` | string | 否 | 登录密码 |
| `keypath` | string | 否 | SSH 私钥路径 |
| `children` | array | 否 | 子主机列表（分组） |

*注：仅当没有 `children` 时需要填写


## 终端行为

SSHM 严格遵循 Unix 终端语义：

- **Cooked 模式**：TUI 界面、SFTP Shell、提示符 - 支持行编辑和 Ctrl+C 信号
- **Raw 模式**：仅在 SSH 交互式 shell 期间 - 所有按键直接转发到远程 PTY

Raw 模式是临时的，在 SSH 会话结束后自动恢复终端状态。

## 键盘快捷键

### TUI 界面
- `↑↓` 或 `kj` - 移动光标
- `Enter` - 确认选择
- `Esc` - 返回/取消
- `/` - 搜索
- `q` / `Ctrl+C` - 退出


## 开发

### 项目结构

```
sshm/
├── main.go                 # 程序入口
├── go.mod                  # Go 模块定义
├── pkg/
│   ├── config/            # 配置解析
│   │   ├── loader.go
│   │   └── types.go
│   ├── ssh/               # SSH 客户端
│   │   ├── auth.go
│   │   ├── client.go
│   │   ├── jump.go
│   │   └── session.go
│   ├── sftp/              # SFTP 客户端
│   │   ├── client.go
│   │   ├── commands.go
│   │   ├── path.go
│   │   └── progress.go
│   ├── terminal/          # 终端管理
│   │   ├── manager.go
│   │   └── sigwinch.go
│   └── tui/               # TUI 界面
│       ├── keys.go
│       ├── model.go
│       └── styles.go
```

### 核心设计原则

1. **终端生命周期管理**：由 `terminal.Manager` 统一管理，禁止在其他地方调用 `term.MakeRaw`
2. **SFTP 路径管理**：本地和远程工作目录完全独立，每次 `cd` 后更新真实路径
3. **错误处理**：所有错误都向上传递，确保终端状态正确恢复

## 许可证

MIT License - 详见 [LICENSE](LICENSE) 文件

## 致谢

- [Bubble Tea](https://github.com/charmbracelet/bubbletea) - TUI 框架
- [Lipgloss](https://github.com/charmbracelet/lipgloss) - 样式库
- [SFTP](https://github.com/pkg/sftp) - SFTP 客户端库
- [sshw](https://github.com/yangzhi1992/sshw) - 灵感来源

## 贡献

欢迎提交 Issue 和 Pull Request！

## 作者
- Opus 4.6
- GLM 4.7
- Kimi K2.5
