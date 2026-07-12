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
| 核心 Agent 交互（submit/cancel/events） | 📋 待实现 |
| Session 管理 | 📋 待实现 |
| 审批流 | 📋 待实现 |
| 模型与配置 | 📋 待实现 |
| 认证（auth/CORS/rate-limit） | 📋 待实现 |
