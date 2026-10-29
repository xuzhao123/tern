// unified diff 中头部和说明没有代码行号；两侧计数仅从真实 hunk 开始。
export function diffLines(file) {
  let oldLine = null, newLine = file.status === "??" && file.diff?.startsWith("+") ? 1 : null;
  let oldRemaining = 0, newRemaining = newLine === null ? 0 : Infinity;
  return String(file.diff || "").split("\n").filter((line, i, lines) => line || i < lines.length - 1).map((text) => {
    // 类型变化可在同一文件内包含两段 patch；后一段文件头不能沿用前一个 hunk 的坐标。
    if (/^diff --(?:git|cc|combined) /.test(text)) { oldLine = newLine = null; oldRemaining = newRemaining = 0; }
    const hunk = /^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@/.exec(text);
    if (hunk) { oldLine = Number(hunk[1]); newLine = Number(hunk[3]); oldRemaining = Number(hunk[2] ?? 1); newRemaining = Number(hunk[4] ?? 1); return { text, kind: "hunk" }; }
    const row = { text, kind: "" };
    if (oldRemaining > 0 && newRemaining > 0 && (text.startsWith(" ") || text === "")) { row.old = oldLine++; row.new = newLine++; oldRemaining--; newRemaining--; }
    else if (newRemaining > 0 && text.startsWith("+")) { row.new = newLine++; newRemaining--; row.kind = "add"; }
    else if (oldRemaining > 0 && text.startsWith("-")) { row.old = oldLine++; oldRemaining--; row.kind = "del"; }
    return row;
  });
}
