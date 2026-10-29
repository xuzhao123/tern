import hljs from "./vendor/highlight-11.12.0.min.js";
import { SOURCE_HIGHLIGHT_LIMIT, SOURCE_TOKEN_CLASSES } from "./source-highlight.js";

const allowedClasses = new Set(SOURCE_TOKEN_CLASSES);
const entities = { "&amp;": "&", "&lt;": "<", "&gt;": ">", "&quot;": '"', "&#x27;": "'", "&#39;": "'" };
function lineTokens(html, source) {
  const lines = [[]], stack = [];
  const append = (escaped) => {
    const text = escaped.replace(/&(amp|lt|gt|quot|#x27|#39);/g, (entity) => entities[entity]);
    const classes = [...new Set(stack.flat())], key = classes.join(" ");
    text.split("\n").forEach((part, index) => {
      if (index) lines.push([]);
      if (!part) return;
      const line = lines.at(-1), previous = line.at(-1);
      if (previous && previous.classes.join(" ") === key) previous.text += part;
      else line.push({ text: part, classes });
    });
  };
  for (let at = 0; at < html.length;) {
    if (html[at] !== "<") {
      const next = html.indexOf("<", at), end = next < 0 ? html.length : next;
      append(html.slice(at, end)); at = end; continue;
    }
    if (html.startsWith("</span>", at)) {
      if (!stack.length) throw new Error("Unexpected closing token");
      stack.pop(); at += 7; continue;
    }
    const open = /^<span class="([A-Za-z0-9 _-]+)">/.exec(html.slice(at));
    if (!open) throw new Error("Unexpected highlight markup");
    // 内嵌语言和 scope 修饰符不进入应用 CSS；仅保留允许的 hljs token 类。
    stack.push(open[1].split(/\s+/).filter((name) => allowedClasses.has(name))); at += open[0].length;
  }
  if (stack.length || lines.map((line) => line.map((token) => token.text).join("")).join("\n") !== source) throw new Error("Highlight text changed");
  return lines;
}

// 导出纯转换便于使用真实源码校验；浏览器仅在 Worker 中调用。
export function highlightText(language, text) {
  const fallback = (note) => ({ lines: null, language, note });
  if (typeof text !== "string") return fallback("无法读取高亮文本，显示原文。");
  if (new TextEncoder().encode(text).byteLength > SOURCE_HIGHLIGHT_LIMIT) return fallback("文件超过 256 KiB，显示原文。");
  if (typeof language !== "string" || !hljs.getLanguage(language)) return fallback("此文件类型暂不支持语法高亮，显示原文。");
  if (hljs.versionString !== "11.12.0") return fallback("语法高亮资源版本不匹配，显示原文。");
  try {
    const result = hljs.highlight(text, { language, ignoreIllegals: true });
    if (result.errorRaised) return fallback("语法高亮失败，显示原文。");
    return { lines: lineTokens(result.value, text), language, note: "" };
  } catch { return fallback("语法高亮失败，显示原文。"); }
}

if (typeof WorkerGlobalScope !== "undefined" && self instanceof WorkerGlobalScope) {
  self.onmessage = ({ data }) => self.postMessage(highlightText(data?.language, data?.text));
}
