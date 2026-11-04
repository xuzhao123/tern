// 文件正文始终由调用方保留；高亮结果只是按行的展示片段。
export const SOURCE_HIGHLIGHT_LIMIT = 256 * 1024;
export const SOURCE_TOKEN_CLASSES = Object.freeze([
  "hljs-addition", "hljs-attr", "hljs-attribute", "hljs-built_in", "hljs-bullet", "hljs-char", "hljs-code", "hljs-comment",
  "hljs-deletion", "hljs-doctag", "hljs-emphasis", "hljs-formula", "hljs-function", "hljs-keyword", "hljs-link", "hljs-literal",
  "hljs-meta", "hljs-name", "hljs-number", "hljs-operator", "hljs-params", "hljs-property", "hljs-punctuation", "hljs-quote",
  "hljs-regexp", "hljs-section", "hljs-selector-attr", "hljs-selector-class", "hljs-selector-id", "hljs-selector-pseudo",
  "hljs-selector-tag", "hljs-string", "hljs-strong", "hljs-subst", "hljs-symbol", "hljs-tag", "hljs-template-tag",
  "hljs-template-variable", "hljs-title", "hljs-type", "hljs-variable",
]);
const allowedClasses = new Set(SOURCE_TOKEN_CLASSES);
const languages = {
  go: "go", js: "javascript", mjs: "javascript", cjs: "javascript", jsx: "javascript",
  ts: "typescript", mts: "typescript", cts: "typescript", tsx: "typescript", json: "json", jsonc: "json",
  html: "xml", htm: "xml", xhtml: "xml", xml: "xml", svg: "xml", xsl: "xml", css: "css", scss: "scss", less: "less",
  yaml: "yaml", yml: "yaml", md: "markdown", markdown: "markdown", sh: "bash", bash: "bash", zsh: "bash",
  py: "python", pyi: "python", rb: "ruby", rs: "rust", c: "c", h: "c", cpp: "cpp", cc: "cpp", cxx: "cpp",
  hpp: "cpp", hh: "cpp", hxx: "cpp", cs: "csharp", java: "java", kt: "kotlin", kts: "kotlin", swift: "swift",
  lua: "lua", php: "php", pl: "perl", pm: "perl", r: "r", sql: "sql", ini: "ini", toml: "ini", mk: "makefile",
  graphql: "graphql", gql: "graphql", diff: "diff", patch: "diff", vb: "vbnet", wat: "wasm", txt: "plaintext", log: "plaintext",
};
export function sourceLanguage(path) {
  const name = String(path || "").split(/[\\/]/).at(-1).toLowerCase();
  if (name === "makefile" || name === "gnumakefile") return "makefile";
  const dot = name.lastIndexOf(".");
  const extension = dot < 0 ? "" : name.slice(dot + 1);
  return Object.hasOwn(languages, extension) ? languages[extension] : null;
}
const plain = (language, note) => ({ lines: null, language, note });

// lines 与 text.split("\n") 对齐（包括末尾空行），不替换原始行文本或引用版本。
export async function highlightSource(path, text) {
  const language = sourceLanguage(path);
  if (typeof text !== "string") return plain(language, "无法读取高亮文本，显示原文。");
  if (!language) return plain(null, "此文件类型暂不支持语法高亮，显示原文。");
  if (language === "plaintext") return plain(language, "");
  if (new TextEncoder().encode(text).byteLength > SOURCE_HIGHLIGHT_LIMIT) return plain(language, "文件超过 256 KiB，显示原文。");
  if (typeof Worker !== "function") return plain(language, "当前浏览器不支持后台语法高亮，显示原文。");
  return new Promise((resolve) => {
    let worker, timer, settled = false;
    const finish = (result) => {
      if (settled) return;
      settled = true; clearTimeout(timer);
      if (worker) { worker.onmessage = worker.onerror = worker.onmessageerror = null; worker.terminate(); }
      resolve(result);
    };
    try {
      worker = new Worker(new URL("./source-highlight-worker.js", import.meta.url), { type: "module" });
      timer = setTimeout(() => finish(plain(language, "语法高亮超时，显示原文。")), 2000);
      worker.onerror = (event) => { event.preventDefault(); finish(plain(language, "语法高亮资源加载失败，显示原文。")); };
      worker.onmessageerror = () => finish(plain(language, "无法读取语法高亮结果，显示原文。"));
      worker.onmessage = ({ data }) => {
        if (!data || data.language !== language || typeof data.note !== "string" || data.lines !== null && !Array.isArray(data.lines)) finish(plain(language, "语法高亮结果无效，显示原文。"));
        else finish(data);
      };
      worker.postMessage({ language, text });
    } catch { finish(plain(language, "无法启动后台语法高亮，显示原文。")); }
  });
}

// 画面经常重画（diff 面板、流式回答），同样的内容只高亮一次，已有结果当场画上，不闪一下原文。paint 收到 lines（高亮不了时不调用）。
const highlighted = new Map();
export function highlightCached(path, text, paint) {
  const key = path + "\0" + text, known = highlighted.get(key);
  if (known === null) return;
  if (Array.isArray(known)) { paint(known); return; }
  if (highlighted.size > 300) highlighted.clear();
  const pending = known || highlightSource(path, text).then((result) => { highlighted.set(key, result.lines); return result.lines; });
  highlighted.set(key, pending);
  pending.then((lines) => { if (lines) paint(lines); });
}

// 高亮器和源文件的字符串都不作为 DOM 标记解析，属性也只接受固定样式类。
export function sourceLineFragment(tokens) {
  const fragment = document.createDocumentFragment();
  for (const token of Array.isArray(tokens) ? tokens : []) {
    if (typeof token?.text !== "string") continue;
    const classes = Array.isArray(token.classes) ? token.classes.filter((name) => allowedClasses.has(name)) : [];
    if (classes.length) {
      const span = document.createElement("span"); span.className = classes.join(" "); span.textContent = token.text; fragment.append(span);
    } else fragment.append(document.createTextNode(token.text));
  }
  return fragment;
}
