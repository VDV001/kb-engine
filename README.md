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

Go 1.26 · стандартная библиотека · golangci-lint · GitHub Actions CI.

## Разработка

```sh
make test        # юнит-тесты
make test-race   # с детектором гонок
make lint        # golangci-lint
make cover       # покрытие
make ci          # полный гейт (как в CI)
```

## Архитектура (Clean Architecture)

```
cmd/kbengine        CLI: флаги → usecase → вывод (без бизнес-логики)
internal/domain     Entities/VO с инвариантами, доменные ошибки (без I/O)
internal/usecase    сценарии + интерфейсы репозиториев (DIP)
internal/adapter    JSON-каталог, рендер отчётов
```

## Лицензия

AGPL-3.0-or-later. См. [LICENSE](LICENSE).
