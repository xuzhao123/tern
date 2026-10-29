import assert from "node:assert/strict";
import { diffLines } from "../../internal/web/page/diff-lines.js";

const rows = diffLines({ diff: "--- a/test\n+++ b/test\n@@ -10,3 +10,3 @@\n keep\n-old\n+new\n end\n\\ No newline at end of file\n@@ -25,0 +26,2 @@\n+first\n+second\n" });
assert.deepEqual(rows.map(({ old, new: next }) => [old ?? null, next ?? null]), [[null,null],[null,null],[null,null],[10,10],[11,null],[null,11],[12,12],[null,null],[null,null],[null,26],[null,27]]);
assert.deepEqual(diffLines({ status: "??", diff: "+one\n+\n+three\n" }).map((r) => r.new), [1,2,3]);
assert.deepEqual(diffLines({ diff: "Binary files a/x and b/x differ\n" }).map((r) => [r.old,r.new]), [[undefined,undefined]]);
console.log("PASS: unified diff line coordinates, metadata, multiple hunks and untracked text");
