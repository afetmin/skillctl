# skillctl

`skillctl` 控制哪些 Agent Skill 会进入模型的初始上下文。首版只实现 Codex adapter，但配置、状态和命令模型没有绑定到单一 Agent。

默认策略是严格白名单：

- Codex 系统 Skill 不受管理。
- 用户和插件 Skill 默认是 `manual`。
- 初始 `implicit` 白名单为空。
- 项目内 `.agents/skills` 默认只读。
- 手动 Skill 仍可直接通过 `$skill-name` 调用。

## 三种状态

| 状态 | 初始上下文 | 模型自动调用 | `$skill-name` |
| --- | --- | --- | --- |
| `implicit` | 包含描述 | 允许 | 允许 |
| `manual` | 不包含常规 Skill 描述 | 不允许 | 允许 |
| `disabled` | 不包含 | 不允许 | 不允许 |

`manual` 通过 Skill 的 `agents/openai.yaml` 设置：

```yaml
policy:
  allow_implicit_invocation: false
```

`disabled` 通过 Codex app-server 的 `skills/config/write` 管理，不直接重写 `~/.codex/config.toml`。

## 构建和安装

要求：Go 1.24+，以及可执行的 `codex` CLI。

```bash
make build
make install
```

默认安装到 `~/.local/bin/skillctl`。可以覆盖安装目录：

```bash
make install PREFIX=/usr/local
```

## 首次使用

初始化只创建配置，不会立即改动已安装 Skill：

```bash
skillctl init
skillctl sync --dry-run
skillctl sync
```

也可以明确要求初始化后立即应用：

```bash
skillctl init --apply
```

策略修改只保证对新 Codex 任务生效。当前任务中已经注入的描述无法被移除；Codex 没有刷新 Skill 列表时需要重启 Codex。

## 常用命令

```bash
# 查看所有 Skill 的实际状态和期望状态
skillctl list

# 查看单个 Skill
skillctl status grill-me

# 设为允许隐式调用
skillctl set code-review implicit
skillctl allow code-review

# 设为仅手动调用
skillctl set grill-me manual
skillctl manual grill-me

# 完全禁用
skillctl set some-skill disabled
skillctl disable some-skill

# 只改配置，不立即同步
skillctl manual grill-me --no-sync

# 检测漂移；发现漂移时退出码为 3
skillctl doctor

# 恢复 skillctl 接管前的策略
skillctl restore grill-me
skillctl restore --all
```

名称唯一时可以使用短名称。同名 Skill 必须使用 `skillctl list` 展示的完整 ID，例如：

```text
codex:user:agents:code-review
codex:plugin:openai-curated-remote:vercel:nextjs
```

## 项目 Skill

项目 Skill 默认不会被修改，防止弄脏仓库。只有显式传入 `--project` 才会管理当前项目：

```bash
skillctl sync --project --dry-run
skillctl manual my-project-skill --project
```

后台 watcher 同样默认忽略项目 Skill。

## 可选后台守护

前台运行：

```bash
skillctl watch
skillctl watch --interval 10s
```

在 macOS 上注册为 LaunchAgent：

```bash
skillctl watch install --dry-run
skillctl watch install
skillctl watch status
skillctl watch uninstall
```

日志保存在 `~/.local/state/skillctl/watch.log`。

## 配置和状态

期望策略保存在：

```text
~/.config/skillctl/config.yaml
```

它不包含绝对路径，可以放进 dotfiles 跨机器同步：

```yaml
version: 1
active_profile: default
defaults:
  invocation: manual
profiles:
  default:
    implicit: []
    disabled: []
adapters:
  codex:
    command: codex
```

本机恢复信息保存在：

```text
~/.local/state/skillctl/state.json
```

状态文件包含本机路径、原始策略、文件指纹和同步时间，不应该跨机器同步。恢复时如果策略文件被外部修改，`skillctl` 会报告冲突，不覆盖新版内容。

## JSON 和退出码

查询及变更命令支持 `--json`：

```bash
skillctl list --json
skillctl sync --dry-run --json
skillctl doctor --json
```

| 退出码 | 含义 |
| --- | --- |
| `0` | 成功或没有漂移 |
| `1` | 操作失败 |
| `2` | 配置或命令参数错误 |
| `3` | 检测到策略漂移 |
| `4` | 写入或恢复冲突 |

## 安全边界

- 修改 YAML 时使用 AST，不使用字符串替换。
- 写入采用同目录临时文件和原子替换。
- 只管理 `policy.allow_implicit_invocation`。
- 首次改动前记录原始 enabled 和 policy 状态。
- `restore` 只恢复 skillctl 接管的字段。
- `.system` 和 admin Skill 永远跳过。
