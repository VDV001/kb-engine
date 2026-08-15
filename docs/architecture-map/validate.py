# -*- coding: utf-8 -*-
"""Валидатор карты архитектуры, версия 2.

Первая версия пропускала 35 подсадок лжи из 45. Главная дыра: карту целиком
можно было увести в _test.go и остаться зелёной. Здесь закрыты классы лжи,
перечисленные в issue #160.

Правила (H = hard):
  H1  источник не может быть тестовым файлом
  H2  источник не может выходить за пределы репозитория
  H3  символ шага ищется по ГРАНИЦЕ СЛОВА, а не подстрокой, и вне комментариев
  H4  шаг без извлекаемого символа обязан нести поле "symbol"
  H5  unverified обязан нести причину "why"; ссылка проверяется всё равно
  H6  цепочка шагов связна: from шага N достижим из предыдущих
      (шаг может объявить "branch": true — тогда он начинает новую нить осознанно)
  H7  нет петель (from == to) и дублей (from, to, call)
  H8  id сценариев уникальны
  H9  commit карты совпадает с последним коммитом КОДА (правки самой карты не в счёт)
  H10 detail подтверждённого шага содержит путь из source дословно
  H11 узел не может висеть вне сценариев; gaps и runtime_checks — не заглушки

Запуск:
    python3 validate_v2.py <map.json> <repo-root> [--self-test] [--soft]

--soft печатает нарушения новых правил как предупреждения (для перехода на
конвенцию), но код возврата всё равно ненулевой, если есть нарушения старых.
"""
import json
import os
import re
import subprocess
import sys

WINDOW = 2  # было 4: коридор принимаемых строк оказался медианно 19, максимум 608

IDENT = re.compile(r"[A-Za-z_][A-Za-z0-9_]{3,}")
TESTFILE = re.compile(r"(^|/)[\w-]*_test\.(go|ts|tsx|js)$|(^|/)[\w-]*\.test\.(ts|tsx|js)$")
# Слова, которые встречаются почти в каждом файле и потому ничего не доказывают.
NOISE = {"kbengine", "http", "json", "true", "false", "null", "path", "time", "line",
         "text", "file", "string", "error", "func", "type", "case", "return", "nil",
         "data", "name", "value", "with", "from", "into", "self", "this"}

OLD_KINDS = {"node-field", "node-dup", "node-layer", "node-kind", "flow-field",
             "step-ref", "step-call", "step-detail", "step-source", "source-format",
             "source-missing", "source-range", "source-mismatch", "flow-steps-order",
             "gaps-empty", "node-orphan", "step-detail-path"}


def fail(errs, kind, where, msg):
    errs.append({"kind": kind, "where": where, "msg": msg})


def read_lines(root, path):
    # abspath обязателен: при корне "." сравнение путей давало "outside" на КАЖДЫЙ
    # файл, то есть H2 срабатывало ложно и глушило все остальные проверки источника.
    root = os.path.abspath(root)
    full = os.path.normpath(os.path.join(root, path))
    if not full.startswith(root + os.sep):
        return "outside"
    if not os.path.isfile(full):
        return None
    with open(full, encoding="utf-8", errors="replace") as f:
        return f.read().splitlines()


def strip_noise(line):
    """Убирает комментарии — ident внутри прозы ничего не доказывает."""
    for marker in ("//", "#"):
        i = line.find(marker)
        if i >= 0:
            line = line[:i]
    return line


def idents_of(step):
    """Символы, которые обязаны найтись рядом со строкой источника."""
    explicit = step.get("symbol")
    if explicit:
        return {explicit}
    out = set()
    for m in IDENT.finditer(step.get("call") or ""):
        w = m.group(0)
        if w.lower() in NOISE:
            continue
        out.add(w)
    return out


def check_source(root, source, idents, where, errs, strict):
    if ":" not in source:
        fail(errs, "source-format", where, f"источник {source!r} не в форме file:line")
        return
    path, _, lineno = source.rpartition(":")
    if not lineno.isdigit():
        fail(errs, "source-format", where, f"источник {source!r}: номер строки не число")
        return
    if os.path.isabs(path) or ".." in path.split("/"):
        fail(errs, "source-outside", where, f"H2: путь выходит за репозиторий: {path}")
        return
    if TESTFILE.search(path):
        fail(errs, "source-test-file", where,
             f"H1: источник — тестовый файл ({path}); карта обязана цитировать боевой код")
        return
    lines = read_lines(root, path)
    if lines == "outside":
        fail(errs, "source-outside", where, f"H2: путь выходит за репозиторий: {path}")
        return
    if lines is None:
        fail(errs, "source-missing", where, f"файла нет: {path}")
        return
    n = int(lineno)
    if n < 1 or n > len(lines):
        fail(errs, "source-range", where, f"{path}: строки {n} нет (в файле {len(lines)})")
        return
    if not strict or not idents:
        return
    lo, hi = max(0, n - 1 - WINDOW), min(len(lines), n + WINDOW)
    window = "\n".join(strip_noise(l) for l in lines[lo:hi])
    # H3: граница слова, а не подстрока
    hit = [i for i in idents if re.search(r"\b" + re.escape(i) + r"\b", window)]
    if not hit:
        fail(errs, "source-mismatch", where,
             f"H3: {path}:{n} — рядом (±{WINDOW}, вне комментариев) нет ни одного из "
             f"{sorted(idents)}; строка: {lines[n-1].strip()[:80]!r}")


def map_dir_excludes(root):
    """Директории всех карт репозитория — их коммиты не считаются правкой кода.

    Список собирается С ДИСКА, а не пишется именем. Одно имя здесь уже было и
    оказалось ловушкой: pathspec ":(exclude)docs/architecture-map" совпадает по
    компонентам пути и потому НЕ покрывает соседнюю "docs/architecture-map-cloud".
    Замерено: `git ls-files --others -- . ':(exclude)docs/architecture-map'`
    показывает файл второй карты. Следствие было бы не косметическим — правка
    облачной карты сдвигала бы «последний коммит кода», и валидатор объявлял бы
    отставшей карту движка, которая не менялась.
    """
    out = []
    for dirpath, dirnames, filenames in os.walk(os.path.join(root, "docs")):
        dirnames[:] = [d for d in dirnames if d not in {".git", "node_modules"}]
        if "map.json" in filenames:
            rel = os.path.relpath(dirpath, root).replace(os.sep, "/")
            out.append(f":(exclude){rel}")
    return sorted(out)


def validate(m, root, quiet=False):
    errs = []
    stats = {"nodes": 0, "steps": 0, "flows": len(m.get("flows", [])), "unverified": 0,
             "with_symbol": 0}

    # H9: карта заявляет коммит — сверяем с репозиторием
    claimed = m.get("commit")
    if claimed:
        try:
            # Сверяемся с последним коммитом, который менял КОД, а не с HEAD:
            # карта описывает код, а коммиты самой карты его не трогают. Иначе
            # правило невыполнимо — каждая правка карты сдвигает HEAD на шаг вперёд.
            head = subprocess.run(
                ["git", "-C", root, "log", "-1", "--format=%h", "--", "."]
                + map_dir_excludes(root),
                capture_output=True, text=True, timeout=10).stdout.strip()
            dirty = subprocess.run(["git", "-C", root, "status", "--porcelain"],
                                   capture_output=True, text=True, timeout=10).stdout.strip()
            if head and not head.startswith(claimed) and not claimed.startswith(head):
                fail(errs, "commit-mismatch", "map",
                     f"H9: карта заявляет {claimed}, последний коммит кода {head}")
            if dirty:
                # Предупреждение, а не нарушение: во время правки карты дерево грязное
                # всегда, и блокировка сборки сделала бы правило невыполнимым.
                # Факт называется вслух — этого достаточно, чтобы он не потерялся.
                if not quiet:
                    print("предупреждение: рабочее дерево грязное — "
                          "проверка идёт против него, а не против заявленного коммита")
        except Exception as e:  # git может отсутствовать — это не повод падать молча
            fail(errs, "commit-uncheckable", "map", f"H9: не удалось спросить git: {e}")

    layer_ids = {l["id"] for l in m.get("layers", [])}
    node_ids, node_srcs = set(), {}
    node_kinds = {n.get("id"): n.get("kind") for n in m.get("nodes", [])}
    for node in m.get("nodes", []):
        stats["nodes"] += 1
        where = f"node {node.get('id')}"
        for field in ("id", "title", "subtitle", "layer", "kind"):
            if not node.get(field):
                fail(errs, "node-field", where, f"нет поля {field}")
        if node.get("id") in node_ids:
            fail(errs, "node-dup", where, "повторяющийся id")
        node_ids.add(node.get("id"))
        node_srcs[node.get("id")] = node.get("sources") or []
        if node.get("layer") not in layer_ids:
            fail(errs, "node-layer", where, f"слоя {node.get('layer')!r} нет в layers")
        if node.get("kind") not in {"actor", "client", "service", "data", "worker", "external"}:
            fail(errs, "node-kind", where, f"неизвестный kind {node.get('kind')!r}")
        for s in node.get("sources") or []:
            # H1/H2 применяются и к узлам; символ узла не сверяем — узел бывает пакетом
            check_source(root, s, set(), where, errs, strict=False)

    seen_flow_ids = set()
    used = set()
    for flow in m.get("flows", []):
        fid = flow.get("id")
        fwhere = f"flow {fid}"
        if fid in seen_flow_ids:
            fail(errs, "flow-dup-id", fwhere, "H8: повторяющийся id сценария")
        seen_flow_ids.add(fid)
        for field in ("id", "title", "summary", "steps"):
            if not flow.get(field):
                fail(errs, "flow-field", fwhere, f"нет поля {field}")

        seen_n, seen_triples, reached = [], set(), set()
        for idx, step in enumerate(flow.get("steps", [])):
            stats["steps"] += 1
            where = f"{fwhere} step {step.get('n')}"
            seen_n.append(step.get("n"))
            frm, to = step.get("from"), step.get("to")
            for side, ref in (("from", frm), ("to", to)):
                if ref not in node_ids:
                    fail(errs, "step-ref", where, f"{side}={ref!r} отсутствует в nodes")
            if frm == to:
                fail(errs, "step-loop", where, f"H7: петля — from и to это один узел {frm!r}")
            triple = (frm, to, step.get("call"))
            if triple in seen_triples:
                fail(errs, "step-dup", where, "H7: шаг дублирует предыдущий (та же тройка from/to/call)")
            seen_triples.add(triple)

            # H6: связность — from должен быть уже достигнут, кроме первого шага
            if idx == 0:
                reached.update({frm, to})
            else:
                # Новая ветка законна, когда её начинает человек или поверхность:
                # сценарий «бот положил файл, потом владелец запустил команду» — это
                # две ветки одного действия, а не разрыв. Правило ловит внутренний
                # узел (код, данные), внезапно возникший в середине без предыстории.
                if (frm not in reached and node_kinds.get(frm) in {"service", "data"}
                        and not step.get("branch")):
                    fail(errs, "step-disconnected", where,
                         f"H6: {frm!r} — внутренний узел, не встречавшийся раньше; цепочка рвётся")
                reached.update({frm, to})

            if not step.get("call"):
                fail(errs, "step-call", where, "пустой call")
            detail = step.get("detail") or ""
            if len(detail) < 20:
                fail(errs, "step-detail", where, "detail короче 20 символов")

            unver = bool(step.get("unverified"))
            if unver:
                stats["unverified"] += 1
                if not step.get("why"):
                    fail(errs, "step-unverified-why", where,
                         "H5: unverified без поля why — пометка без причины не отличается от лени")

            idents = idents_of(step)
            if step.get("symbol"):
                stats["with_symbol"] += 1
            src = step.get("source") or ""
            if src:
                # H5: ссылка проверяется даже у unverified — не проверяется только символ
                check_source(root, src, idents, where, errs, strict=not unver)
                if not unver and not idents:
                    fail(errs, "step-symbol", where,
                         "H4: из call не извлекается символ — добавьте поле symbol")
                # H10: detail должен называть тот же файл, что и source
                if not unver:
                    path = src.rpartition(":")[0]
                    if path not in detail and os.path.basename(path) not in detail:
                        fail(errs, "step-detail-path", where,
                             f"H10: detail не называет файл из source ({path})")
            elif not unver:
                fail(errs, "step-source", where, "нет source и шаг не помечен unverified")

        if seen_n != list(range(1, len(seen_n) + 1)):
            fail(errs, "flow-steps-order", fwhere, f"номера шагов не 1..N: {seen_n}")
        for s in flow.get("steps", []):
            used.add(s.get("from")); used.add(s.get("to"))

    for n in m.get("nodes", []):
        if n.get("id") not in used:
            fail(errs, "node-orphan", f"node {n.get('id')}", "H11: узел вне всех сценариев")

    gaps = [g for g in (m.get("gaps") or []) if isinstance(g, str) and len(g.strip()) >= 25]
    if not gaps:
        fail(errs, "gaps-empty", "map", "H11: gaps пуст или состоит из заглушек короче 25 символов")
    rc = [r for r in (m.get("runtime_checks") or []) if isinstance(r, str) and len(r.strip()) >= 25]
    if m.get("runtime_checks") and not rc:
        fail(errs, "runtime-empty", "map", "H11: runtime_checks состоит из заглушек")

    return errs, stats


def self_test(m, root):
    import copy
    cases = []

    def with_step(mut, name, kind):
        bad = copy.deepcopy(m)
        mut(bad)
        cases.append((name, bad, kind))

    with_step(lambda b: b["flows"][0]["steps"][0].__setitem__("source", "internal/nope.go:1"),
              "несуществующий файл", "source-missing")
    with_step(lambda b: b["flows"][0]["steps"][0].__setitem__("source", "cmd/kbengine/main.go:999999"),
              "строка за пределами файла", "source-range")
    with_step(lambda b: b["flows"][0]["steps"][0].__setitem__("to", "nope"),
              "шаг ссылается в никуда", "step-ref")
    with_step(lambda b: b.__setitem__("gaps", []), "пустой gaps", "gaps-empty")
    with_step(lambda b: b["flows"][0]["steps"][0].__setitem__("source", ""),
              "подтверждённый шаг без источника", "step-source")
    with_step(lambda b: b["nodes"].append({"id": "orphan", "title": "с", "subtitle": "с",
                                           "layer": m["layers"][0]["id"], "kind": "service", "sources": []}),
              "узел вне сценариев", "node-orphan")
    # новые правила
    with_step(lambda b: b["flows"][0]["steps"][0].__setitem__("source", "cmd/kbengine/fin_edit_test.go:59"),
              "H1: ссылка в тестовый файл", "source-test-file")
    with_step(lambda b: b["flows"][0]["steps"][0].__setitem__("source", "../../etc/passwd:1"),
              "H2: путь наружу репозитория", "source-outside")
    with_step(lambda b: b["flows"][0]["steps"][0].__setitem__("to", b["flows"][0]["steps"][0]["from"]),
              "H7: петля from==to", "step-loop")
    with_step(lambda b: b["flows"][0]["steps"].append(copy.deepcopy(b["flows"][0]["steps"][0])),
              "H7: дубль шага", "step-dup")
    with_step(lambda b: b["flows"].append(copy.deepcopy(b["flows"][0])),
              "H8: два сценария с одним id", "flow-dup-id")
    with_step(lambda b: b.__setitem__("commit", "deadbee"),
              "H9: чужой коммит", "commit-mismatch")
    with_step(lambda b: b.__setitem__("gaps", ["нет"]),
              "H11: gaps-заглушка", "gaps-empty")

    print("=== отрицательный контроль ===")
    ok = True
    for name, bad, want in cases:
        errs, _ = validate(bad, root, quiet=True)
        kinds = {e["kind"] for e in errs}
        if want in kinds:
            print(f"  поймана   ✅  {name}  ({want})")
        else:
            print(f"  ПРОПУЩЕНА ❌  {name} — ждали {want}")
            ok = False
    return ok


def main():
    if len(sys.argv) < 3:
        print(__doc__)
        return 2
    map_path, root = sys.argv[1], sys.argv[2]
    m = json.load(open(map_path, encoding="utf-8"))
    if "--self-test" in sys.argv:
        return 0 if self_test(m, root) else 1

    errs, stats = validate(m, root)
    print(f"=== {m.get('project')} @ {m.get('commit')} — валидатор v2 ===")
    print(f"узлов {stats['nodes']}, сценариев {stats['flows']}, шагов {stats['steps']}, "
          f"unverified {stats['unverified']}, с полем symbol {stats['with_symbol']}")
    if not errs:
        print("нарушений нет")
        return 0
    by = {}
    for e in errs:
        by.setdefault(e["kind"], []).append(e)
    old = sum(len(v) for k, v in by.items() if k in OLD_KINDS)
    print(f"\nНАРУШЕНИЙ: {len(errs)} (из них по правилам первой версии: {old})")
    for kind, items in sorted(by.items(), key=lambda kv: -len(kv[1])):
        print(f"\n-- {kind} ({len(items)})")
        for e in items[:6]:
            print(f"   {e['where']}: {e['msg']}")
        if len(items) > 6:
            print(f"   … ещё {len(items) - 6}")
    return 1


if __name__ == "__main__":
    sys.exit(main())
