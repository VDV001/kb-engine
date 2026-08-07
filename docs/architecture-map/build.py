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
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.abspath(os.path.join(HERE, "..", ".."))
MAP = os.path.join(HERE, "map.json")
TPL = os.path.join(HERE, "template.html")
OUT = os.path.join(HERE, "index.html")
PLACEHOLDER = "__DATA__"


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
    if PLACEHOLDER not in tpl:
        print(f"build: в шаблоне нет {PLACEHOLDER}", file=sys.stderr)
        return 1

    with open(OUT, "w", encoding="utf-8") as f:
        f.write(tpl.replace(PLACEHOLDER, data))
    print(f"build: {os.path.relpath(OUT, REPO)} собран ({os.path.getsize(OUT)} байт)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
