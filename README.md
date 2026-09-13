# belltune

铸钟刮削调音评估 API — Go + Gin 纯后端，无前端、无数据库、无外部依赖服务。

## API

### `POST /assess`

请求体：`application/x-www-form-urlencoded`，五个字段各出现且仅出现一次：

| 字段 | 含义 | 约束 |
|---|---|---|
| `hum` | 哼音 Hz | 有限正数，且 20 ≤ hum ≤ 1000（边界含） |
| `prime` | 主音 Hz | 有限正数 |
| `tierce` | 三度音 Hz | 有限正数 |
| `quint` | 五度音 Hz | 有限正数 |
| `nominal` | 名义音 Hz | 有限正数 |

目标频率依次为 `hum`、`2×hum`、`2.4×hum`、`3×hum`、`4×hum`。
偏差 = `1200×log2(实测/目标)` 音分；容差 hum ±5，其余 ±8，**边界计为合格**。
裁决使用**未舍入**偏差；响应中的展示值四舍五入到小数点后两位，负零显示为 `0.00`。

**200 响应**（分音按固定顺序 hum → prime → tierce → quint → nominal）：

```json
{
  "partials": [
    {"name": "hum",     "target_hz": "100.00", "measured_hz": "100.00", "deviation_cents": "0.00",  "tolerance_cents": 5, "result": "pass"},
    {"name": "prime",   "target_hz": "200.00", "measured_hz": "200.15", "deviation_cents": "+1.30", "tolerance_cents": 8, "result": "pass"},
    {"name": "tierce",  "target_hz": "240.00", "measured_hz": "240.00", "deviation_cents": "0.00",  "tolerance_cents": 8, "result": "pass"},
    {"name": "quint",   "target_hz": "300.00", "measured_hz": "301.60", "deviation_cents": "+9.23", "tolerance_cents": 8, "result": "fail"},
    {"name": "nominal", "target_hz": "400.00", "measured_hz": "400.00", "deviation_cents": "0.00",  "tolerance_cents": 8, "result": "pass"}
  ],
  "summary": {"verdict": "fail", "out_of_tune": ["quint"]}
}
```

`summary.verdict` 只有在全部分音合格时才为 `pass`；否则为 `fail`，且
`out_of_tune` 按固定顺序列出所有越界分音——即下一轮需要继续刮削的振型组。

**422 响应**（任一字段缺失、重复、不可解析、NaN、无穷、非正数或 hum 越界；
整次请求被拒绝，不给出调音结论）：

```json
{
  "error": "invalid_fields",
  "fields": [
    {"field": "hum", "reason": "out_of_range"},
    {"field": "quint", "reason": "not_finite"}
  ]
}
```

`reason` 取值：`missing`、`duplicate`、`unparseable`、`not_finite`、
`not_positive`、`out_of_range`（仅 hum）。表单整体无法解析时返回
`{"error": "invalid_form", "fields": []}`。

### `GET /healthz`

存活探针，返回 `{"status": "ok"}`。

## 本地运行与测试

```sh
go test ./...        # 标准库测试：正负边界、舍入不改变裁决、非法浮点等
go run .             # 监听 :8080（可用 PORT 覆盖）
curl -X POST localhost:8080/assess \
  -d 'hum=100&prime=200&tierce=240&quint=300&nominal=400'
```

## Docker / Compose

```sh
API_PORT=9000 docker compose up api              # 宿主端口由 API_PORT 覆盖（默认 8080）
docker compose up --exit-code-from verify verify # 一次性验收服务，全部通过则退出码 0
```

镜像构建时会先执行 `go vet` 与全部测试，测试不过则构建失败。
