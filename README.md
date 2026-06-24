# kb-engine

Движок для персональной базы знаний (knowledge base): загрузка каталога записей,
аудиты жизненного цикла и «стюардшип» (дубликаты, дрейф, свежесть), генерация
отчётов. Data-agnostic — работает с любой KB, путь задаётся флагом, личные данные
в репозиторий не входят.

> Переписан с нуля на Go по TDD + DDD + Clean Architecture. Предыстория и принципы —
> в [docs/adr/0001-architecture.md](docs/adr/0001-architecture.md).

## Статус

Ранняя разработка (v0.1, вертикальный срез). Не готов к продакшену.

## Стек

Go 1.26 · стандартная библиотека · golangci-lint v2 · just · GitHub Actions CI.

## Разработка

```sh
just             # список рецептов
just test        # юнит-тесты
just test-race   # с детектором гонок
just lint        # golangci-lint
just cover       # покрытие
just ci          # полный гейт (как в CI)
```

## Архитектура (Clean Architecture)

```
cmd/kbengine        CLI: флаги → usecase → вывод (без бизнес-логики)
internal/domain     Entities/VO с инвариантами, доменные ошибки (без I/O)
internal/usecase    сценарии + интерфейсы репозиториев (DIP)
internal/adapter    JSON-каталог, рендер отчётов
```

## Лицензия

MIT. См. [LICENSE](LICENSE).
