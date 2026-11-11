## ⚠️ Kestrel verify: partially_resolved

**observed** · window 30s every 2s · session `01M76TESTSESSION` · incident #2 · live

| | Check | Result | Detail |
|---|---|---|---|
| ✅ | api probe `http://127.0.0.1:8080/health` | passed 15/15 |  |
| ❌ | web probe `http://127.0.0.1:5173/` | failed 0/15 | ``status 503 \| body: <script>alert(1)</script> `ticks` [link](http://evil) @everyone`` |
| ⚠️ | tcp `127.0.0.1:5432` | unstable 9/15 | `connection refused` |
| ❌ | command `make test` | failed | exit code 2 |
| ❌ | command `sleep 99` | timed out |  |
| ➖ | command `./slow-check.sh` | not run | not authorized: pass --allow-exec to run it (no terminal to ask in) |
| ➖ | &lt;img src=x onerror=alert(1)&gt; probe `http://127.0.0.1:1/` | not run | skipped: &lt;b&gt;bold&lt;/b&gt; \| pipe |

**Output of command** `make test`

````text
FAIL: TestX
bad  byte & <tag>
``` escape attempt
# heading
@mention
````

**Why**

- failing: web (probe http://127.0.0.1:5173/), command `make test`
- the affected services logged 7 new error event(s) during the window

<details><summary>Still occurring during the window (2)</summary>

- `web: line one line two \| <b>bold</b> ˋˋˋfenceˋˋˋ` ×6
- `api: connection refused to 127.0.0.1:5432`

</details>

<details><summary>Before: what was failing (1)</summary>

- `web: crashed (exit code 1)`

</details>

**Notes**

- an error repeating unchanged is recorded as a summary every 2s

