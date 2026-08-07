# -*- coding: utf-8 -*-
"""Строгий валидатор карты архитектуры.

Проверяет карту ПРОТИВ РЕПОЗИТОРИЯ, а не против себя: каждая ссылка file:line
открывается, и в окрестности строки ищется имя, которое шаг заявляет вызовом.

Запуск:
    python3 validate_map.py <map.json> <repo-root>
    python3 validate_map.py <map.json> <repo-root> --self-test

--self-test подсаживает в копию карты заведомо ложные записи и требует, чтобы
валидатор поймал КАЖДУЮ. Без этого «валидатор прошёл» не значит ничего: проверка,
которая ничего не проверяет, и проверка, которой нечего сказать, снаружи выглядят
одинаково.
"""
import json
import os
import re
import sys

WINDOW = 4  # строк вверх и вниз от указанной — на случай сдвига сигнатуры

# Из call достаём идентификаторы, которые обязаны встретиться рядом с указанной
# строкой. Берём последний сегмент после точки и слова длиннее трёх букв.
IDENT = re.compile(r"[A-Za-z_][A-Za-z0-9_]{3,}")
# Признак того, что detail несёт путь к файлу: что-то.go / .ts / .tsx, возможно с :line
PATH_IN_DETAIL = re.compile(r"[\w./-]+\.(go|ts|tsx|js|py|sh)(:\d+)?")


def fail(errs, kind, where, msg):
    errs.append({"kind": kind, "where": where, "msg": msg})


def read_lines(root, path):
    full = os.path.join(root, path)
    if not os.path.isfile(full):
        return None
    with open(full, encoding="utf-8", errors="replace") as f:
        return f.read().splitlines()


def check_source(root, source, expect_idents, where, errs, strict_ident=True):
    """Открывает file:line и ищет в окрестности хотя бы один ожидаемый идентификатор."""
    if ":" not in source:
        fail(errs, "source-format", where, f"источник {source!r} не в форме file:line")
        return False
    path, _, lineno = source.rpartition(":")
    if not lineno.isdigit():
        fail(errs, "source-format", where, f"источник {source!r}: номер строки не число")
        return False
    lines = read_lines(root, path)
    if lines is None:
        fail(errs, "source-missing", where, f"файла нет: {path}")
        return False
    n = int(lineno)
    if n < 1 or n > len(lines):
        fail(errs, "source-range", where,
             f"{path}: строки {n} не существует (в файле {len(lines)})")
        return False
    if not expect_idents or not strict_ident:
        return True
    lo, hi = max(0, n - 1 - WINDOW), min(len(lines), n + WINDOW)
    window = "\n".join(lines[lo:hi])
    hit = [i for i in expect_idents if i in window]
    if not hit:
        fail(errs, "source-mismatch", where,
             f"{path}:{n} — рядом нет ни одного из {sorted(expect_idents)}; "
             f"строка: {lines[n-1].strip()[:90]!r}")
        return False
    return True


def idents_of(call):
    out = set()
    for m in IDENT.finditer(call or ""):
        w = m.group(0)
        if w.lower() in {"kbengine", "http", "json", "true", "false", "null", "path"}:
            continue
        out.add(w)
    return out


def validate(m, root):
    errs = []
    stats = {"nodes": 0, "nodes_with_source": 0, "steps": 0, "steps_with_source": 0,
             "steps_unverified": 0, "flows": len(m.get("flows", []))}

    layer_ids = {l["id"] for l in m.get("layers", [])}
    node_ids = set()

    for node in m.get("nodes", []):
        stats["nodes"] += 1
        where = f"node {node.get('id')}"
        for field in ("id", "title", "subtitle", "layer", "kind"):
            if not node.get(field):
                fail(errs, "node-field", where, f"нет поля {field}")
        if node.get("id") in node_ids:
            fail(errs, "node-dup", where, "повторяющийся id")
        node_ids.add(node.get("id"))
        if node.get("layer") not in layer_ids:
            fail(errs, "node-layer", where, f"слоя {node.get('layer')!r} нет в layers")
        if node.get("kind") not in {"actor", "client", "service", "data", "worker", "external"}:
            fail(errs, "node-kind", where, f"неизвестный kind {node.get('kind')!r}")
        srcs = node.get("sources") or []
        if srcs:
            stats["nodes_with_source"] += 1
            for s in srcs:
                # Для узла ищем его собственное имя из subtitle/title, но мягко:
                # узел может быть пакетом, а не функцией.
                check_source(root, s, set(), where, errs, strict_ident=False)

    for flow in m.get("flows", []):
        fwhere = f"flow {flow.get('id')}"
        for field in ("id", "title", "summary", "steps"):
            if not flow.get(field):
                fail(errs, "flow-field", fwhere, f"нет поля {field}")
        seen_n = []
        for step in flow.get("steps", []):
            stats["steps"] += 1
            where = f"{fwhere} step {step.get('n')}"
            seen_n.append(step.get("n"))
            for side in ("from", "to"):
                ref = step.get(side)
                if ref not in node_ids:
                    fail(errs, "step-ref", where, f"{side}={ref!r} отсутствует в nodes")
            if not step.get("call"):
                fail(errs, "step-call", where, "пустой call")
            detail = step.get("detail") or ""
            if len(detail) < 20:
                fail(errs, "step-detail", where, "detail короче 20 символов — это не объяснение")
            unver = bool(step.get("unverified"))
            if unver:
                stats["steps_unverified"] += 1
            src = step.get("source") or ""
            if src:
                stats["steps_with_source"] += 1
                # Строгая проверка идентификатора — только для подтверждённых шагов.
                check_source(root, src, idents_of(step.get("call")), where, errs,
                             strict_ident=not unver)
            elif not unver:
                fail(errs, "step-source", where,
                     "нет source и шаг не помечен unverified — так карта врёт молча")
            if not unver and not PATH_IN_DETAIL.search(detail):
                fail(errs, "step-detail-path", where,
                     "в detail подтверждённого шага нет пути к файлу")
        if seen_n != list(range(1, len(seen_n) + 1)):
            fail(errs, "flow-steps-order", fwhere, f"номера шагов не 1..N: {seen_n}")

    gaps = m.get("gaps") or []
    if not gaps:
        fail(errs, "gaps-empty", "map", "раздел gaps пуст — обязателен по условию задачи")

    return errs, stats


def self_test(m, root):
    """Подсадка заведомо ложных записей: валидатор ОБЯЗАН поймать каждую."""
    import copy
    cases = []

    # 1. Ссылка на несуществующий файл.
    bad = copy.deepcopy(m)
    bad["flows"][0]["steps"][0]["source"] = "internal/nope/nothing.go:10"
    cases.append(("несуществующий файл", bad, "source-missing"))

    # 2. Строка за пределами файла.
    bad = copy.deepcopy(m)
    bad["flows"][0]["steps"][0]["source"] = "cmd/kbengine/main.go:999999"
    cases.append(("строка за пределами файла", bad, "source-range"))

    # 3. Настоящий файл и строка, но заявленного вызова там нет.
    bad = copy.deepcopy(m)
    bad["flows"][0]["steps"][0]["call"] = "SomethingNeverDefinedAnywhere"
    bad["flows"][0]["steps"][0]["unverified"] = False
    cases.append(("вызов не найден рядом со строкой", bad, "source-mismatch"))

    # 4. Ссылка на несуществующий узел.
    bad = copy.deepcopy(m)
    bad["flows"][0]["steps"][0]["to"] = "node-which-does-not-exist"
    cases.append(("шаг ссылается в никуда", bad, "step-ref"))

    # 5. Пустые gaps.
    bad = copy.deepcopy(m)
    bad["gaps"] = []
    cases.append(("пустой раздел gaps", bad, "gaps-empty"))

    # 6. Подтверждённый шаг без source.
    bad = copy.deepcopy(m)
    bad["flows"][0]["steps"][0]["source"] = ""
    bad["flows"][0]["steps"][0]["unverified"] = False
    cases.append(("подтверждённый шаг без источника", bad, "step-source"))

    print("=== отрицательный контроль: ловит ли валидатор подсаженное ===")
    ok = True
    for name, bad_map, want in cases:
        errs, _ = validate(bad_map, root)
        kinds = {e["kind"] for e in errs}
        if want in kinds:
            print(f"  поймана   ✅  {name}  ({want})")
        else:
            print(f"  ПРОПУЩЕНА ❌  {name}  — ждали {want}, получили {sorted(kinds) or 'ничего'}")
            ok = False
    return ok


def main():
    if len(sys.argv) < 3:
        print(__doc__)
        return 2
    map_path, root = sys.argv[1], sys.argv[2]
    with open(map_path, encoding="utf-8") as f:
        m = json.load(f)

    if "--self-test" in sys.argv:
        return 0 if self_test(m, root) else 1

    errs, stats = validate(m, root)
    print(f"=== карта: {m.get('project')} @ {m.get('commit')} ===")
    print(f"узлов {stats['nodes']}, из них с file:line — {stats['nodes_with_source']} "
          f"({stats['nodes'] - stats['nodes_with_source']} без)")
    print(f"сценариев {stats['flows']}, шагов {stats['steps']}, "
          f"из них с source — {stats['steps_with_source']}, "
          f"помечено unverified — {stats['steps_unverified']}")
    print()
    if not errs:
        print("нарушений нет")
        return 0
    by_kind = {}
    for e in errs:
        by_kind.setdefault(e["kind"], []).append(e)
    print(f"НАРУШЕНИЙ: {len(errs)}")
    for kind, items in sorted(by_kind.items()):
        print(f"\n-- {kind} ({len(items)})")
        for e in items:
            print(f"   {e['where']}: {e['msg']}")
    return 1


if __name__ == "__main__":
    sys.exit(main())
