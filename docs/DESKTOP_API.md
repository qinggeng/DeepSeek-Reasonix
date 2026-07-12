# Desktop HTTP API

桌面版 Reasonix 内嵌的 HTTP API，供第三方工具、自动化脚本和 MCP 客户端通过 REST/SSE 控制桌面版实例。

> **Base URL**: `http://127.0.0.1:7777/api`（默认，全局配置 `desktop.api_port` 可改）
> **Content-Type**: `application/json`

---

## Endpoints

### Workspace

#### 列出所有 Workspace

```http
GET /api/workspaces
```

**响应 `200`**：

```json
[
  {"path": "C:\\Projects\\foo", "name": "foo", "current": true},
  {"path": "C:\\Projects\\bar", "name": "bar", "current": false}
]
```

| 字段 | 类型 | 说明 |
|------|------|------|
| `path` | string | 项目根目录绝对路径 |
| `name` | string | 显示名称（目录名） |
| `current` | boolean | 是否为当前活跃 workspace |

---

#### 切换 / 新建 Workspace

如果路径尚未注册，会自动注册并初始化 `.reasonix/` 配置目录。

```http
POST /api/workspaces
Content-Type: application/json

{"path": "C:/Projects/foo"}
```

**响应 `200`**：

```json
{"path": "C:\\Projects\\foo", "name": "foo", "current": true}
```

**响应 `400`**（路径不可用）：

```json
{"error": "directory not accessible: C:\\Projects\\foo"}
```

| 错误原因 | 说明 |
|----------|------|
| 路径不存在 | 目录不存在或不可读 |
| 路径不可写 | 无法在路径下创建 `.reasonix/` 配置目录 |
| `path` 为空 | 请求体缺少 `path` 字段或为空字符串 |

---

#### 移除 Workspace

```http
DELETE /api/workspaces/{url_encoded_path}
```

路径需要 URL 编码。例：`C:\Projects\foo` → `C%3A%5CProjects%5Cfoo`

**响应 `200`**：

```json
{"success": true}
```

**响应 `404`**：

```json
{"error": "workspace not found"}
```

---

### Topic

#### 列出 Topic

```http
GET /api/workspaces/{url_encoded_path}/topics
```

路径需要 URL 编码。例：`C:\Projects\foo` → `/api/workspaces/C%3A%5CProjects%5Cfoo/topics`

**响应 `200`**：

```json
[
  {
    "id": "topic_20260712-002505_9ce58ef6087ee1a4",
    "title": "新的会话",
    "kind": "topic",
    "workspaceRoot": "C:\\Projects\\foo",
    "sessionPath": "C:\\Projects\\foo\\.reasonix\\sessions\\xxx.jsonl",
    "pinned": false
  }
]
```

| 字段 | 类型 | 说明 |
|------|------|------|
| `id` | string | Topic 标识符 |
| `title` | string | 显示标题（自动生成或手动重命名） |
| `kind` | string | 类型：`"topic"`（项目）或 `"global_topic"`（全局）|
| `workspaceRoot` | string | 所属 workspace 路径 |
| `sessionPath` | string | 最后活跃的 session 文件路径 |
| `pinned` | boolean | 是否置顶 |

---

#### 激活 Topic

切换到指定 topic 的会话（等同于在左侧树点击 topic）。

```http
POST /api/topics/{topic_id}/activate
```

**响应 `200`**：

```json
{
  "id": "topic_dev",
  "title": "开发",
  "kind": "topic",
  "workspaceRoot": "C:\\Projects\\foo",
  "sessionPath": "C:\\Projects\\foo\\.reasonix\\sessions\\dev.jsonl",
  "pinned": false
}
```

**响应 `404`**：

```json
{"error": "topic not found: nonexistent"}
```

---

#### 查询 Topic 状态

```http
GET /api/topics/{topic_id}/status
```

**响应 `200`**（空闲）：

```json
{
  "running": false,
  "model": "deepseek-v4",
  "effort": "medium",
  "hasPendingPrompt": false
}
```

**响应 `200`**（运行中）：

```json
{
  "running": true,
  "model": "deepseek-v4",
  "effort": "high",
  "hasPendingPrompt": false,
  "currentStep": "正在分析文件结构..."
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| `running` | boolean | 是否正在运行 agent |
| `model` | string | 当前使用的模型名称 |
| `effort` | string | 当前 reasoning effort 级别 |
| `hasPendingPrompt` | boolean | 是否有待处理的审批 prompt |
| `currentStep` | string | 当前执行步骤描述（仅 running 时存在） |

**响应 `404`**：

```json
{"error": "topic not found: nonexistent"}
```

---

## 审批流

审批流允许第三方客户端通过 HTTP API 响应 Agent 的审批请求，支持工具调用审批、多选问答、审批模式设置。

> Agent 发出 `ApprovalRequest` / `AskRequest` SSE 事件后阻塞等待回复。客户端通过以下端点回复后 agent 恢复运行。

---

### 审批/拒绝工具调用

```http
POST /api/workspaces/{path}/topics/{id}/approve
Content-Type: application/json

{"id": "approval-uuid", "allow": true, "session": true}
```

**参数**：

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `id` | string | 是 | SSE `ApprovalRequest` 事件的 `approvalId` |
| `allow` | boolean | 是 | `true` 批准 / `false` 拒绝 |
| `session` | boolean | 否 | 当前 session 内记住此决定（默认 `true`） |
| `persist` | boolean | 否 | 持久化到配置（默认 `false`） |

**响应 `200`**：

```json
{"status": "approved", "approvalId": "approval-uuid", "allow": true}
```

---

### 回答多选问题

```http
POST /api/workspaces/{path}/topics/{id}/answer
Content-Type: application/json

{"id": "ask-uuid", "answers": [{"questionId": "q1", "selected": ["Option A"]}]}
```

**参数**：

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `id` | string | 是 | SSE `AskRequest` 事件的 `askId` |
| `answers[].questionId` | string | 是 | 问题 ID |
| `answers[].selected` | string[] | 是 | 用户选择的选项 label |

**响应 `200`**：

```json
{"status": "answered", "askId": "ask-uuid", "count": 1}
```

---

### 查询待审批状态

```http
GET /api/workspaces/{path}/topics/{id}/pending
```

**响应 `200`**：

```json
{"pending": true}
```

`pending` 为 `true` 表示 agent 正在等待审批或问答回复，为 `false` 表示无待处理项。

---

### 设置审批模式

```http
POST /api/workspaces/{path}/topics/{id}/approval-mode
Content-Type: application/json

{"mode": "ask"}
```

**参数**：

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `mode` | string | 是 | `"ask"` 每次询问 / `"auto"` 自动批准低风险 / `"yolo"` 自动批准全部 |

**响应 `200`**：

```json
{"status": "ok", "mode": "ask"}
```

---

### 重放待审批 prompt

```http
POST /api/approval/replay
```

Agent 启动后可能已有 pending 审批（比如重启恢复 session）。调用此端点使所有活跃 tab 重新发出 `ApprovalRequest` / `AskRequest` SSE 事件。

**响应 `200`**：

```json
{"status": "ok"}
```

---

## 通用错误

| HTTP 状态码 | 说明 |
|-------------|------|
| `400` | 请求参数错误（无效 body、缺少字段、路径不可用） |
| `404` | 资源不存在（workspace / topic 未找到） |
| `405` | 方法不允许（如 GET 调了 POST 端点） |
| `500` | 服务器内部错误 |

所有错误响应格式：

```json
{"error": "描述信息"}
```

---

## 实现状态

| 域 | 状态 |
|----|------|
| Workspace 管理 | ✅ 已实现（Sprint 1） |
| Topic 管理 | ✅ 已实现（Sprint 1） |
| 核心 Agent 交互（submit/cancel/events） | ✅ 已实现（Sprint 2） |
| Session 管理 | ✅ 已实现（Sprint 3） |
| 审批流 | ✅ 已实现（Sprint 4） |
| 模型与配置 | 📋 待实现 |
| 认证（auth/CORS/rate-limit） | 📋 待实现 |
