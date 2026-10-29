import assert from 'node:assert/strict';
import { highlightText } from '../../internal/web/page/source-highlight-worker.js';
import { sourceLanguage } from '../../internal/web/page/source-highlight.js';
for (const [path,source] of [['example.js','const 中文 = "<script>alert(1)</script>";\n// & < >\n'],['example.go','package main\nfunc main() { println("你好") }\n'],['example.html','<script>window.BAD=true</script>\n<div title="&quot;">&amp;</div>\n'],['example.py','def main():\n    print("中文")\n']]) {
 const result=highlightText(sourceLanguage(path),source); assert.ok(result.lines,result.note); assert.equal(result.lines.map(line=>line.map(t=>t.text).join('')).join('\n'),source);
}
assert.equal(highlightText('javascript','x'.repeat(256*1024+1)).lines,null);
assert.equal(highlightText('unknown','text').lines,null);
console.log('PASS: local highlighter preserves exact UTF-8 source, HTML entities, line breaks, limit and unsupported-language fallback');
