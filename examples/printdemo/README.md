# printdemo

Manual test program for goprint. It runs on every platform; this page
lists what to check on **macOS**, where the print panel cannot be tested
in CI.

```sh
git clone https://github.com/timzifer/goprint && cd goprint
go run ./examples/printdemo -mode list
```

Headless printing (`-mode print`) needs an explicit `-printer`, so nothing
goes to the default printer by accident. In the dialog, prefer
**"PDF" → "Save as PDF"** (bottom left of the panel) to check results
without paper.

## Checklist (macOS)

| # | Command | Expected |
|---|---|---|
| 1 | `-mode list` | All printers, `*` marks the default. |
| 2 | `-mode caps -printer NAME` | Paper sizes, duplex/color, `preview in dialog true`. |
| 3 | `-mode dialog` | Panel **with preview** of the 5-page A4 document comes to the front. "Abbrechen" → prints `canceled`. |
| 4 | `-mode dialog -copies 2 -landscape -media iso_a5_148x210mm -pages 2-3` | Panel opens with 2 copies, landscape, A5, pages 2–3 preset (the preview follows). "PDF → Als PDF sichern": the PDF has 2 pages, A5 landscape. Output lists the chosen settings. |
| 5 | `-mode dialog -printer NAME` | NAME is preselected. |
| 6 | `-mode settings -landscape -copies 3` | Panel **without preview**; "Drucken" prints nothing, output shows the chosen settings and `no job (settings only)`. Change some values in the panel and check they come back. |
| 7 | `-mode dialog`, then print on a real printer | Output shows a job id (`job "…"`), `job state: completed` once printed. |
| 8 | `-mode print -printer NAME -pages 1` | Headless (IPP/CUPS): one page printed, job state completed. |
| 9 | `-mode dialog -pdf some.pdf` | Your own PDF in the preview. |

Please note for each step: worked / what happened, and anything odd
(panel behind other windows, app icon in the Dock, hangs, crashes).

## Flags

```
-mode list|caps|dialog|settings|print
-printer NAME          -pdf FILE
-copies N              -landscape
-media PWG-NAME        -pages A-B | A- | A
-duplex none|long|short
-mono                  -require-printer
-timeout 10m
```
