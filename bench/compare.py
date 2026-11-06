#!/usr/bin/env python3
"""Compare model URLs sequentially with the same inputs, CPU settings, temperature 0 and EOS stopping."""

import argparse
import json
import os
import re
import signal
import statistics
import subprocess
import sys
import time


CASES = [
    ("business", "ja", "en", "会議は来週の月曜日に変更されました。金曜日までに資料を送っていただけると助かります。", []),
    ("technical", "en", "ja", "The cache is invalidated only after the transaction commits. If the commit fails, the previous value must remain unchanged.", ["-form", "plain"]),
    ("numbers", "en", "ja", "Revenue fell by 12.5%, from $8 million to $7 million. This does not mean that operating profit decreased.", []),
    ("negation", "ja", "en", "この機能は管理者にしか無効化できません。設定を変更しても、すでに開始した処理は停止しません。", []),
    ("context", "ja", "en", "かみを切りました。", ["-context", "工作で折り紙を作っています。"]),
    ("glossary", "en", "ja", "Open the dashboard. You can filter tasks by owner.", ["-form", "polite", "-context", "プロジェクト管理ツールのヘルプページです。", "-glossary", "dashboard:管理画面", "-glossary", "owner:担当者"]),
]


LONG_CASES = [
    ("long_operations", "en", "ja", """Project HARBOR-742 will replace the warehouse scheduling service on 2027-03-18. The change concerns reservation records, not the software that controls conveyor belts. Operations approved the plan because duplicate reservations delayed shipments last month. A successful database migration alone does not authorize the team to reopen the loading gate.

The rehearsal processed 1860 reservations in an isolated environment. Engineers copied account identifiers but removed customer names before exporting the records. The copy must not be sent to the analytics vendor, even if the vendor requests it for debugging. Only the internal incident team may inspect the unmodified export.

Ticket CACHE-593 records a separate cache defect. A worker publishes an invalidation event only after its transaction commits. If the transaction fails, the old value must remain available. Retrying the event is safe because consumers reject duplicate event identifiers; retrying the payment request is not covered by that guarantee.

During deployment, keep the legacy endpoint available for 47 minutes. New requests should use the replacement service, except for accounts whose contracts require the old response format. Those accounts remain on the legacy route until their owners approve a schema change. Do not interpret a successful health check as customer approval.

The dashboard shows delayed jobs separately from failed jobs. A delayed job can still finish without intervention, whereas a failed job requires an owner to review its error. In this document, owner means the person responsible for the task, not the legal owner of the warehouse. Preserve that distinction in customer instructions.

Finance approved a ceiling of USD 28300 for temporary capacity. This amount excludes taxes and does not permit additional permanent licenses. If the trial ends early, unused capacity must be released before the next billing period. Savings should be calculated from actual usage rather than from the approved ceiling.

Audit package AUDIT-826 is due on 2027-04-09. It must contain the signed checklist and a redacted event log. Screenshots are optional unless the incident involved an incorrect status display. Missing screenshots alone must not block approval when the checklist and logs establish what happened.

The service manager will decide whether to continue after reviewing latency and support reports together. Lower latency is not sufficient if customers cannot recover canceled reservations. If either review identifies an unresolved recovery defect, postpone expansion and explain the reason to the regional team before announcing a revised schedule.""",
     ["-form", "polite", "-context", "倉庫システム移行の運用手順です。",
      "-glossary", "dashboard:管理画面", "-glossary", "owner:担当者"]),
    ("long_rollout", "ja", "en", """案件SAKURA-918は、2028-06-14に始まる地域図書館の予約サービス更新である。今回の対象は本の受取予約であり、電子書籍の貸出機能は変更しない。利用者の待ち時間を減らすための更新だが、画面表示が速くなっただけでは完了と判定しない。予約の取消しと返却後の再予約まで確認する必要がある。

事前調査では、受付端末に保存された2760件の操作記録を確認した。同じ利用者が連続してボタンを押した記録は、別々の予約として数えない。ただし、異なる資料を選び直した操作は除外しない。氏名と住所は分析用の記録から削除し、元の記録を外部の委託先へ渡すことは禁止する。

障害票QUEUE-465に記載された遅延は、通信速度ではなく、更新通知を送る順番が原因だった。データベースへの保存が確定してから通知を送ること。保存に失敗した場合、以前の予約状態を残さなければならない。通知の再送は認めるが、利用料金の請求まで再実行してよいという意味ではない。

切替後も旧システムを53分間利用できるようにしておく。通常の予約は新システムへ送るが、支援機器で旧画面を使う利用者は例外とする。この例外を終了するには担当者の確認が必要であり、自動監視が正常だったという理由だけで経路を閉じてはならない。窓口職員にも例外の扱いを共有する。

管理画面では「保留」と「失敗」を別の状態として表示する。保留中の処理は追加操作なしで完了する可能性があるが、失敗した処理は担当者による確認が必要である。この文書の担当者は、その予約に責任を持つ職員を指し、資料の所有者ではない。利用者向けの説明でもこの区別を保つこと。

臨時の機器借用に使える予算の上限はJPY 68400とする。この金額には配送料を含むが、恒久ライセンスの購入費は含まない。試行を早く終えた場合は、次の請求期間が始まる前に機器を返却する。予算の上限と実際の支出の差を、そのまま更新による削減額として報告してはならない。

確認資料REPORT-357の提出期限は2028-07-22である。署名済みの点検表と、個人情報を除いた操作記録を添付する。画面の写真は必須ではないが、誤った状態表示が発生した場合に限り必要となる。点検表と記録で経緯が確認できるなら、写真がないことだけを理由に承認を止めない。

本格導入の判断では、応答時間と窓口への相談内容を合わせて検討する。取消した予約を利用者が復元できない場合、平均応答時間が改善していても対象館を増やさない。責任者は未解決の問題と延期の理由を地域の連絡会へ説明し、その後で新しい日程を公表する。日程を先に約束して確認作業を省略しないこと。""",
     ["-context", "公共図書館の予約システム導入手順です。",
      "-glossary", "管理画面:dashboard", "-glossary", "担当者:owner"]),
]

# Literal, non-overlapping markers: formatting changes can fail these checks too.
MARKERS = {
    "long_operations": ["HARBOR-742", "2027-03-18", "1860", "CACHE-593", "47",
                        "28300", "AUDIT-826", "2027-04-09"],
    "long_rollout": ["SAKURA-918", "2028-06-14", "2760", "QUEUE-465", "53",
                     "68400", "REPORT-357", "2028-07-22"],
}
DEFAULT_URL = "https://huggingface.co/Qwen/Qwen3-4B-GGUF/resolve/bc640142c66e1fdd12af0bd68f40445458f3869b/Qwen3-4B-Q4_K_M.gguf"


def fact_checks(name, original, translation):
    markers = MARKERS.get(name, [])
    counts = {}
    positions = []
    for marker in markers:
        # ASCII boundaries allow Japanese suffixes but reject 47 inside 147.
        pattern = rf"(?<![A-Za-z0-9]){re.escape(marker)}(?![A-Za-z0-9])"
        matches = list(re.finditer(pattern, translation))
        counts[marker] = {"source": len(re.findall(pattern, original)), "output": len(matches)}
        positions.extend(match.start() for match in matches)
    exact = all(row["source"] == row["output"] for row in counts.values())
    return {"markers": counts,
            "exact_occurrences": exact if markers else None,
            "same_order": (exact and positions == sorted(positions)) if markers else None}


def diagnostics(stderr):
    chunks = [(int(i), int(total)) for i, total in
              re.findall(r"Translating chunk (\d+)/(\d+)\.\.\.", stderr)]
    rss = re.search(r"^\s*(\d+)\s+maximum resident set size\s*$", stderr, re.MULTILINE)
    total = chunks[0][1] if chunks else None
    return {"peak_rss_bytes": int(rss[1]) if rss else None,
            "chunks": {"reported_total": total, "observed": len(chunks),
                       "sequence": chunks,
                       "all_announced": (chunks == [(i, total) for i in range(1, total + 1)])
                       if chunks else None}}


def save_output(report, path):
    if path:
        with open(path + ".tmp", "w", encoding="utf-8") as output:
            json.dump(report, output, ensure_ascii=False, indent=2)
            output.write("\n")
        os.replace(path + ".tmp", path)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--cli", default="./bin/local-translate")
    parser.add_argument("--runs", type=int, default=3)
    parser.add_argument("--long-runs", type=int, default=1)
    parser.add_argument("--urls", nargs="+", default=[DEFAULT_URL])
    parser.add_argument("--cases", choices=["short", "long", "all"], default="short")
    parser.add_argument("--threads", type=int, default=8)
    parser.add_argument("--ctx", type=int, default=8192, help="context tokens for all models (default: 8192)")
    parser.add_argument("--max-tokens", type=int, default=256, help="output tokens per chunk; use 4096 for long runs")
    parser.add_argument("--timeout", type=float, default=1800, help="seconds per process (default: 1800)")
    parser.add_argument("--output", help="incrementally replace this JSON file after every run; also emit JSON to stdout")
    args = parser.parse_args()
    if min(args.runs, args.long_runs, args.threads) < 1 or not (0 < args.timeout < float("inf")):
        parser.error("runs, long-runs, threads and timeout must be positive")
    if not 1 <= args.max_tokens < args.ctx <= 32768:
        parser.error("require 1 <= max-tokens < ctx <= 32768")
    if len(args.urls) != len(set(args.urls)):
        parser.error("urls must not contain duplicates")

    cases = (CASES if args.cases != "long" else []) + (LONG_CASES if args.cases != "short" else [])
    use_time = sys.platform == "darwin" and os.access("/usr/bin/time", os.X_OK)
    environment = {key: value for key, value in os.environ.items() if not key.startswith("LOCAL_TRANSLATE_")}
    results = []
    report = {"threads": args.threads, "ctx": args.ctx, "max_tokens": args.max_tokens,
              "runs": args.runs, "long_runs": args.long_runs, "cases": args.cases,
              "timeout": args.timeout,
              "timing": "per-process wall seconds including loading; no cold-cache control",
              "memory": "per-process peak RSS in bytes from macOS /usr/bin/time -l" if use_time else "unavailable",
              "generation": "model-independent CLI defaults: deterministic temperature 0 and EOS stopping",
              "checks": "literal fact-preservation checks, not translation quality scores; chunk announcements do not prove completion",
              "model_urls": args.urls, "results": results}
    save_output(report, args.output)
    for name, source, target, text, options in cases:
        rows = {label: [] for label in args.urls}
        case = {"case": name, "source": source, "target": target, "text": text,
                "options": options, "models": {label: {"runs": runs, "median_seconds": None}
                                                for label, runs in rows.items()}}
        results.append(case)
        # Rotate the selected model order across repetitions.
        for repetition in range(args.long_runs if name in MARKERS else args.runs):
            order = list(args.urls)
            offset = repetition % len(order)
            order = order[offset:] + order[:offset]
            for label in order:
                command = [args.cli, "-debug", "-model-url", label, "-threads", str(args.threads),
                           "-ctx", str(args.ctx), "-max-tokens", str(args.max_tokens),
                           "-from", source, "-to", target, *options]
                measured_command = ["/usr/bin/time", "-l", *command] if use_time else command
                start = time.perf_counter()
                error = None
                returncode = None
                stdout = stderr = ""
                try:
                    with subprocess.Popen(measured_command, stdin=subprocess.PIPE,
                                          stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                          text=True, encoding="utf-8", errors="replace",
                                          start_new_session=True, env=environment) as process:
                        try:
                            stdout, stderr = process.communicate(text, timeout=args.timeout)
                        except (subprocess.TimeoutExpired, KeyboardInterrupt) as exc:
                            # Kill the time wrapper AND its CLI child, not just the wrapper.
                            try:
                                os.killpg(process.pid, signal.SIGKILL)
                            except ProcessLookupError:
                                pass
                            stdout, stderr = process.communicate()
                            if isinstance(exc, KeyboardInterrupt):
                                raise
                            error = f"process timed out after {args.timeout}s"
                        returncode = process.returncode
                    if returncode:
                        error = error or f"process exited with status {returncode}"
                    if not stdout.strip():
                        error = error or "translation output is empty"
                except OSError as exc:
                    error = str(exc)
                elapsed = time.perf_counter() - start
                rows[label].append({"seconds": elapsed, "translation": stdout, "stderr": stderr,
                                    "returncode": returncode, "error": error, "command": command,
                                    **diagnostics(stderr),
                                    "fact_preservation": fact_checks(name, text, stdout)})
                successful = [r["seconds"] for r in rows[label] if r["error"] is None]
                case["models"][label]["median_seconds"] = statistics.median(successful) if successful else None
                save_output(report, args.output)
                print(f"{name}: {label} run {repetition + 1}: {elapsed:.3f}s ({error or 'ok'})",
                      file=sys.stderr, flush=True)
    json.dump(report, sys.stdout, ensure_ascii=False, indent=2)
    print()


if __name__ == "__main__":
    main()
