# -*- coding: utf-8 -*-
"""Сборка страницы карты: template.html + map.json → index.html.

Данные и вёрстка держатся врозь намеренно: карту правят по коду (map.json),
страницу — по внешнему виду (template.html). Одним файлом они бы разошлись
при первой же правке одного из двух.

    python3 docs/architecture-map/build.py

Перед сборкой прогоняется валидатор: страница, собранная из карты с битой
ссылкой file:line, выглядит ровно так же убедительно, как правильная.
"""
import json
import os
import re
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.abspath(os.path.join(HERE, "..", ".."))
MAP = os.path.join(HERE, "map.json")
TPL = os.path.join(HERE, "template.html")
VALIDATOR = os.path.join(HERE, "validate.py")
OUT = os.path.join(HERE, "index.html")
PLACEHOLDER = "__DATA__"
CASES = "__SELFTEST_CASES__"

# Число случаев отрицательного контроля называется на странице. Держать его в
# вёрстке отдельной копией уже пробовали: копия говорила «шесть», когда случаев
# стало тринадцать, и никакая проверка этого не видела — валидатор смотрит на
# данные, а не на прозу шаблона. Теперь число считается в самом валидаторе.
def selftest_cases():
    with open(VALIDATOR, encoding="utf-8") as f:
        n = len(re.findall(r"^\s*with_step\(", f.read(), re.M))
    if n == 0:
        raise SystemExit("build: в валидаторе не нашлось ни одного случая отрицательного контроля")
    return n


def main():
    rc = subprocess.call([sys.executable, os.path.join(HERE, "validate.py"), MAP, REPO])
    if rc != 0:
        print("build: валидатор нашёл нарушения — страница не собрана", file=sys.stderr)
        return rc

    with open(MAP, encoding="utf-8") as f:
        data = json.dumps(json.load(f), ensure_ascii=False, separators=(",", ":"))
    # Закрывающий тег внутри данных разорвал бы <script> и сломал страницу молча.
    if "</script" in data:
        print("build: в данных есть закрывающий тег script", file=sys.stderr)
        return 1

    with open(TPL, encoding="utf-8") as f:
        tpl = f.read()
    for token in (PLACEHOLDER, CASES):
        if token not in tpl:
            print(f"build: в шаблоне нет {token}", file=sys.stderr)
            return 1

    page = tpl.replace(PLACEHOLDER, data).replace(CASES, str(selftest_cases()))
    with open(OUT, "w", encoding="utf-8") as f:
        f.write(page)
    print(f"build: {os.path.relpath(OUT, REPO)} собран ({os.path.getsize(OUT)} байт)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
