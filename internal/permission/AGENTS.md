# permission 的约束

- 权限按用户规则（deny › ask › allow）与权限模式（plan、ask、edit、full，默认 edit）决定；规则和模式只能由用户给。询问与高风险确认走同一条 `tool/requestApproval`。
- full 模式不再询问，但照常判断、写审计后由程序放行；用户的 ask 规则与 hook 要求的确认仍然询问。
