# free_proxy

中文文档。英文版本请见 [README.md](README.md)。

`free_proxy` 是一个轻量级的 Go 库和命令行工具，用于收集公开代理列表、并发验证可用性，并将可用结果导出为 JSON 或 TXT 文件。

## 功能特性

- 从多个公开源站收集代理。
- 使用内置默认源站或通过 JSON 配置文件加载源站定义。
- 通过命令行参数配置抓取并发、验证并发、超时和重试行为。
- 在验证前对代理记录进行去重和标准化。
- 并发验证 HTTP、HTTPS 和 SOCKS5 候选代理，并支持达到目标后提前停止。
- 可选地为有效代理补充地区信息。
- 当个别源站超时或失败时，继续处理其余健康源站返回的数据。
- 在运行结束时输出每个源站的抓取与验证汇总。
- 支持交互模式和适合脚本调用的命令行模式。

## 快速开始

```bash
go run ./cmd/main.go
```

当不传任何参数时，会自动进入交互模式。

非交互模式示例：

```bash
go run ./cmd/main.go \
  --count 50 \
  --valid-count 20 \
  --timeout 15s \
  --fetch-concurrency 12 \
  --validate-concurrency 150 \
  --fetch-retries 4 \
  --fetch-retry-delay 300ms \
  --source-config ./sources.json \
  --unfiltered-format json \
  --valid-format txt \
  --output-dir ./dist \
  --check-region \
  --only-ip
```

如果某个上游源站超时或返回错误，命令行程序会记录该源站失败信息，并继续处理其他健康源站抓到的代理。只有顶层上下文被取消（例如按下 Ctrl+C）时，程序才会提前终止。

`--count` 用于限制进入验证阶段的代理数量。`--valid-count` 用于在找到足够多有效代理后提前停止验证，并输出一份顺序稳定、可重复的结果集。

## CLI 参数

| 参数                     | 默认值  | 说明                                                             |
| ------------------------ | ------- | ---------------------------------------------------------------- |
| `--interactive`          | `false` | 运行交互式流程。未传任何参数时也会自动进入交互模式。             |
| `--count`                | `0`     | 进入验证阶段的已收集代理数量上限；`0` 表示使用全部已收集代理。   |
| `--valid-count`          | `0`     | 找到指定数量的可用代理后提前停止验证；`0` 表示验证全部候选代理。 |
| `--unfiltered-format`    | `json`  | 未过滤代理列表的输出格式，可选 `json` 或 `txt`。                 |
| `--valid-format`         | `json`  | 已验证代理列表的输出格式，可选 `json` 或 `txt`。                 |
| `--output-dir`           | `.`     | 输出文件写入目录。                                               |
| `--check-region`         | `false` | 为已验证代理查询地区元数据。                                     |
| `--check-type-https`     | `true`  | 在验证期间检测代理协议类型以及是否支持 HTTPS。                   |
| `--only-ip`              | `false` | 当 `--valid-format=txt` 时，仅输出每行一个 `ip:port`。           |
| `--timeout`              | `20s`   | 源站抓取和地区查询使用的请求超时时间。                           |
| `--fetch-concurrency`    | `8`     | 同时运行的源站抓取请求最大数量。                                 |
| `--validate-concurrency` | `100`   | 同时运行的代理验证检查最大数量。                                 |
| `--fetch-retries`        | `3`     | 每个源站页面的最大抓取尝试次数。                                 |
| `--fetch-retry-delay`    | `500ms` | 两次抓取重试之间的基础退避延迟。                                 |
| `--source-config`        | `""`    | 用于覆盖内置源站列表的 JSON 配置文件路径。                       |

## 源站配置文件

你可以通过 `--source-config <path>` 覆盖内置源站列表。配置文件必须是一个 JSON 数组，数组中的每个元素表示一个源站对象：

```json
[
  {
    "name": "custom_text_source",
    "url": "https://example.com/proxies/{page}",
    "pages": 2,
    "kind": "api_text",
    "default_type": "HTTP",
    "default_https": "Unchecked"
  }
]
```

支持的 `kind` 取值为 `api_text`、`api_json` 和 `web`。如果使用 `web` 类型，还需要设置 `web_parser`，可选值包括 `freeproxylist`、`proxy_list_plus`、`89ip` 和 `sslproxies`。

## 项目结构

- `cmd/main.go`：命令行入口。
- `service.go`：工作流编排。
- `network.go`：抓取、验证、SOCKS5 和地区查询逻辑。
- `parsers.go`：各源站解析辅助函数。
- `output.go`：文件序列化。
- `metadata.go`：可复用的元数据辅助函数，便于下游集成。

## 开发

推荐 Go 版本：`1.25.0`

```bash
go test ./...
```

如果所有源站都失败，或者最终没有收集到任何唯一代理，程序会直接退出，不生成代理输出文件。
