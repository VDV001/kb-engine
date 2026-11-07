#!/usr/bin/env bash
# Сборка под все ОС выпуска до тега. CI и pre-push собирали только для своей
# машины, и 07.11 тег v0.37.0 упал в goreleaser: syscall.SysProcAttr{Setsid} в
# cmd/kbengine/creds.go под Windows не собирается, а узнали об этом только на
# выпуске — страницы выпуска нет, номер сгорел.
#
# Список ОС читается из .goreleaser.yml (goos: [...]), а не держится здесь:
# новая цель выпуска не пройдет мимо гейта. Сборка — как у goreleaser,
# CGO_ENABLED=0. Архитектура одна (amd64): расходятся по ОС, а не по arm64.
#
# Использование:
#   cross-build.sh              собрать ./cmd/... под каждую ОС выпуска
#   cross-build.sh --self-test  список ОС обязан прочитаться и содержать windows
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

release_oses() {
	grep -ohE 'goos: \[[^]]+\]' .goreleaser.y*ml | sed -E 's/goos: \[//; s/\]//; s/,/ /g' | tr ' ' '\n' | sed '/^$/d' | sort -u
}

if [[ ${1:-} == --self-test ]]; then
	oses="$(release_oses | tr '\n' ' ')"
	[[ $oses == *windows* && $oses == *linux* && $oses == *darwin* ]] || {
		echo "self-test: из .goreleaser.yml прочитано «$oses» — разбор списка ОС сломан" >&2
		exit 1
	}
	echo "✓ cross-build --self-test: ОС выпуска — $oses"
	exit 0
fi

fail=0
for os in $(release_oses); do
	if ! out="$(CGO_ENABLED=0 GOOS="$os" GOARCH=amd64 go build -o /dev/null ./cmd/... 2>&1)"; then
		echo "✘ сборка под $os не прошла:"
		printf '%s\n' "$out"
		fail=1
	fi
done
if ((fail)); then
	echo "✘ выпуск под эту ОС упадет в goreleaser — нужен файл с //go:build для нее"
	exit 1
fi
echo "✓ cross-build: ./cmd/... собирается под $(release_oses | tr '\n' ' ')"
