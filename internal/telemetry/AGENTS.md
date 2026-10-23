# telemetry 的约束

- 用 OpenTelemetry Go SDK，属性按 GenAI 语义约定，span 写 `.data/traces/`；提示词与工具内容默认不采集，外部平台只通过 `OTEL_*` 环境变量接入。
