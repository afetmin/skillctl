# skillctl

`skillctl` 管理 Codex 和 Claude 的个人、项目 Skill 调用状态。每次命令只处理一个 Agent；两个 Agent 的清单、配置、恢复日志和同步结果不会混在一起。

插件 Skill、系统 Skill、管理员 Skill 和未知来源不在产品范围内，既不展示也不修改。

## Agent 与状态

没有 `--agent` 时，skillctl 检测配置中的命令是否已安装，固定按 Codex、Claude 的顺序选择。两个都存在时选择 Codex。`--agent` 只影响当前命令，不会改变 watcher：

```bash
skillctl --agent codex list
skillctl --agent claude list
```

| skillctl 状态 | Codex | Claude `skillOverrides` |
| --- | --- | --- |
| `implicit` | 启用，允许隐式调用 | `on` |
| `name-only` | 不支持 | `name-only` |
| `manual` | 启用，禁止隐式调用 | `user-invocable-only` |
| `disabled` | 禁用 | `off` |

Codex 的 `manual` 写入 Skill 自己的 `agents/openai.yaml`；启用与禁用通过 Codex app-server 的 `skills/config/write` 按绝对路径写入。Claude 只修改 settings JSON 中目标 Skill 对应的一个 `skillOverrides` 字段，并保留其他设置。

## 支持的目录

| Agent | 个人 Skill | 项目 Skill |
| --- | --- | --- |
| Codex | `~/.agents/skills/*/SKILL.md`、`~/.codex/skills/*/SKILL.md` | 从当前目录到仓库根的 `.agents/skills/*/SKILL.md` |
| Claude | `~/.claude/skills/*/SKILL.md` | 从当前目录到仓库根的 `.claude/skills/*/SKILL.md` |

不会向下扫描当前工作目录之外的项目子目录。项目 Skill 默认只读，只有带 `--project` 的命令才会同步、恢复或删除它们。

Claude 同名 Skill 按个人优先于项目、仓库根优先于更深项目目录处理。胜出的定义可操作，其余定义仍显示为 shadowed 且只读。实际状态按项目本地、项目共享、用户、默认值的顺序计算。项目写入只落到 `.claude/settings.local.json`，不会修改共享的 `.claude/settings.json`。

Claude Managed Settings 暂不参与有效状态计算；检测到时 `list`、`doctor` 和 TUI 会明确警告。Claude 的 settings 与 Skills 说明见[官方文档](https://code.claude.com/docs/en/settings)。

## 构建和安装

要求 Go 1.24.2+，以及至少一个可执行的 `codex` 或 `claude` 命令。

```bash
make build
make install
```

默认安装到 `~/.local/bin/skillctl`，可以用 `PREFIX=/usr/local` 覆盖。

## 首次使用

首次运行 TUI 会创建最终版配置，但不会立即修改 Skill。也可以显式执行：

```bash
skillctl init
skillctl sync --dry-run
skillctl sync
```

skillctl 只接受 `version: 2` 的最终配置。旧格式不会自动解析或迁移；升级时先备份，再手动把仍受支持的个人、项目策略放入对应 Agent 段，并删除旧插件记录。

## 常用命令

```bash
skillctl                         # 打开 TUI
skillctl list
skillctl list --scope personal
skillctl list --scope project --project
skillctl status grill-me

skillctl allow grill-me
skillctl manual grill-me
skillctl disable grill-me
skillctl --agent claude name-only grill-me
skillctl set grill-me name-only --agent claude

skillctl sync --dry-run
skillctl sync --project --dry-run
skillctl doctor
skillctl restore grill-me
skillctl restore --all --project
```

名称唯一时可以用短名称；同名 Skill 使用 `list` 输出的完整 ID，例如：

```text
codex:user:agents:code-review
claude:user:claude:code-review
claude:repo:my-repo:code-review
```

删除只对 TUI 当前选中的个人或已启用项目管理的 Skill 开放。删除目录符号链接时只删除链接本身；期望策略、Claude override 和恢复记录会保留为 orphan，重新安装同一 Skill 后再次生效。

## TUI

顶部 Agent 控件是完整上下文切换，不是清单过滤器。按 `c` 或在 Agent 控件上使用左右键切换；切换后重新加载该 Agent 的清单、有效状态、期望 profile 和恢复日志，并更新 watcher 目标。

有暂存修改时，切换前必须选择 Apply and switch、Discard and switch 或 Cancel。Codex 只显示 `implicit`、`manual`、`disabled`，Claude 额外显示 `name-only`。

| 按键 | 操作 |
| --- | --- |
| `c` | 切换 Agent |
| `Tab` | 在 Agent、状态、来源和表格之间切换焦点 |
| `j` / `k`、方向键、鼠标 | 移动选择 |
| `i` / `n` / `m` / `d` | 暂存为 implicit / name-only / manual / disabled |
| `a` | 应用全部暂存修改 |
| `u` | 清空暂存修改 |
| `Esc` | 撤销当前 Skill 的暂存修改 |
| `x` | 删除当前可管理 Skill |
| `/` | 搜索 |
| `r` | 刷新 |
| `o` | 用 `$EDITOR` 打开 `SKILL.md` |
| `h` | 帮助 |
| `q` | 退出 |

## Watcher

```bash
skillctl watch
skillctl watch --project
skillctl watch install --dry-run
skillctl watch install
skillctl watch status
skillctl watch uninstall
```

watcher 目标保存在独立的 `runtime.json` 中。只有完成的 TUI Agent 切换会修改它；普通 `--agent` 不会。切换发生在当前同步结束后，新 Agent 会立即同步。单个 Skill 冲突不会阻止其他有效变更，watcher 会记录 incomplete 并继续运行。

## 配置和状态

便携期望配置：

```yaml
version: 2
agents:
  codex:
    command: codex
    active_profile: default
    defaults:
      invocation: manual
    profiles:
      default:
        implicit: []
        manual: []
        disabled: []
  claude:
    command: claude
    active_profile: default
    defaults:
      invocation: manual
    profiles:
      default:
        implicit: []
        name_only: []
        manual: []
        disabled: []
```

两个 Agent 的 `defaults.invocation` 可以独立设置，但必须是该 Agent 支持的状态。profile 可用 `manual` 等显式 selector 覆盖各自默认值。

默认文件：

```text
~/.config/skillctl/config.yaml
~/.local/state/skillctl/codex.json
~/.local/state/skillctl/claude.json
~/.local/state/skillctl/runtime.json
~/.local/state/skillctl/watch.log
```

两个 Agent 的恢复日志完全分开。Claude 记录 override 原来是否存在、原值和 skillctl 最后写入值；恢复原本不存在的 override 时会删除该 key，而不是写一个显式默认值。

## JSON 和退出码

`list`、`status`、`sync`、`doctor`、`restore` 和变更命令支持 `--json`。同步报告包含 Agent 身份，并分别列出 `applied_changes`、`skipped_changes` 和 `conflicted_changes` 及逐 Skill 原因。

| 退出码 | 含义 |
| --- | --- |
| `0` | 成功或没有漂移 |
| `1` | 操作失败 |
| `2` | 配置或命令参数错误 |
| `3` | 检测到漂移或 orphan |
| `4` | 部分同步或恢复冲突 |

所有 settings、policy、配置和日志写入都使用同目录临时文件与原子替换。恢复只操作 skillctl 接管的字段，发现同字段被外部修改时不会覆盖。
