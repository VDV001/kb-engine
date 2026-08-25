#!/usr/bin/env bash
# Локальный прогон и CI — два описания одной проверки, и расходятся они молча.
# До этого гейта justfile объявлял `ci: tidy lint test-race cover-gate gates`
# подписью «Full gate, same as CI», проверяя три джобы из двенадцати.
#
# Гейт берёт состав CI С ДИСКА (секция jobs: во всех workflow), а таблицу
# соответствий держит у себя — и ругается в ОБЕ стороны:
#   джоба есть, записи нет  → новая проверка приехала в CI мимо локального прогона;
#   запись есть, джобы нет  → локально гоняем то, чего в CI давно не существует.
#
# Джоба, которую здесь воспроизвести нечем, объявляется вслух с причиной, а не
# пропускается молча: «проверять нечем» и «проверено» — разные ответы.
#
# Использование:
#   ci-parity.sh              состав: что воспроизводится, что нет
#   ci-parity.sh --run        то же и выполнить воспроизводимое
#   ci-parity.sh --self-test  проверка проверки: две подсадки, обе обязаны упасть
set -euo pipefail

WORKFLOWS=${CI_PARITY_WORKFLOWS:-.github/workflows}

# Таблица соответствий: "<файл>:<джоба>|<вид>|<пояснение>"
#   local  — воспроизводится здесь, пояснение печатается человеку
#   absent — воспроизвести нечем, пояснение это ПРИЧИНА
#   n/a    — джоба не проверяет изменения (выпуск, автомерж бота)
# Команды живут в run_job ниже: держать их в строке таблицы значило бы
# экранировать кавычки в кавычках при каждой правке.
TABLE=(
	"ci.yml:build|local|форматирование, линтер, тесты с гонками, порог покрытия 80%"
	"ci.yml:frontend|local|npm ci, oxlint, гейт слоёв данных, vitest, сборка бандла"
	"ci.yml:bench|local|бенчмарки горячих путей с -benchmem и проверкой, что хоть один прогнан"
	"ci.yml:dep-age|local|самопроверка гейта возраста и он сам против origin/main"
	"ci.yml:timezones|local|самопроверка матрицы поясов и тесты в UTC+14 и UTC-11"
	"ci.yml:changelog-scope|local|самопроверка гейта журнала и он сам"
	"ci.yml:ci-parity|local|самопроверка этого гейта и сверка состава"
	"ci.yml:arch-map|local|самопроверка валидатора карт и он сам"
	"ci.yml:capabilities|local|самопроверка гейта таблицы возможностей и сверка с README"
	"ci.yml:govulncheck|local|govulncheck по всем пакетам"
	"ci.yml:gitleaks|local|gitleaks по всей истории"
	"ci.yml:offline|local|самопроверка гейта прогона без сети и он сам (нужен докер)"
	"ci.yml:docker|local|сборка образа + verify-claims (размер из README); ⚠️ скан trivy не воспроизводится — trivy не установлен"
	"ci.yml:terraform|absent|нет opentofu и tflint, а джоба поднимает LocalStack в докере"
	"ci.yml:k8s|absent|нужен kind (только через nix-shell -p kind), джоба поднимает кластер"
	"codeql.yml:analyze|absent|нужен CodeQL CLI, на машине его нет"
	"dependabot-auto-merge.yml:auto-merge|n/a|автомерж обновлений бота, изменения не проверяет"
	"release.yml:goreleaser|n/a|сборка выпуска по тегу"
	"release.yml:docker|n/a|публикация образа выпуска"
)

# Джобы читаются с диска: ключи с отступом в два пробела внутри секции jobs:.
# Греп по '^  [a-z-]+:' не годится — под него попадает триггер push: из on:.
jobs_on_disk() {
	local f
	for f in "$WORKFLOWS"/*.yml; do
		[[ -e $f ]] || continue
		awk -v file="$(basename "$f")" '
			/^jobs:/ { inside = 1; next }
			inside && /^[a-zA-Z]/ { inside = 0 }
			inside && /^  [a-zA-Z0-9_-]+:[[:space:]]*$/ {
				gsub(/[ :]/, "", $0); print file ":" $0
			}
		' "$f"
	done
}

lookup() { # печатает "вид|пояснение" или пусто
	local key=$1 row
	for row in "${TABLE[@]}"; do
		[[ ${row%%|*} == "$key" ]] && { echo "${row#*|}"; return 0; }
	done
	return 0 # промах — не ошибка: о нём отчитывается report, назвав джобу
}

run_job() {
	case $1 in
	ci.yml:bench)
		[[ -d frontend/dist ]] || (cd frontend && npm ci && npm run build)
		go test -run '^$' -bench . -benchmem -benchtime 200x ./... | tee /tmp/kbengine-bench.txt
		grep -q "^Benchmark" /tmp/kbengine-bench.txt || { echo "ни один бенчмарк не прогнан"; return 1; }
		;;
	ci.yml:build)
		[[ -d frontend/dist ]] || (cd frontend && npm ci && npm run build)
		[[ -z $(gofmt -l .) ]] || { echo "gofmt: файлы не отформатированы"; return 1; }
		golangci-lint run
		go test -race -coverprofile=coverage.out ./...
		go tool cover -func=coverage.out | awk '/^total:/ {
			total = substr($3, 1, length($3) - 1)
			printf "total coverage: %s%%\n", total
			if (total + 0 < 80.0) { print "coverage below 80% threshold"; exit 1 }
		}'
		;;
	ci.yml:frontend)
		(cd frontend && npm ci && npx oxlint src && npx vitest run && npm run build)
		./scripts/gates/web-data-layer.sh
		;;
	ci.yml:offline)
		./scripts/gates/offline.sh --self-test
		./scripts/gates/offline.sh
		;;
	ci.yml:dep-age)
		./scripts/gates/dep-age.sh --self-test
		./scripts/gates/dep-age.sh "$(git merge-base origin/main HEAD)"
		;;
	ci.yml:timezones)
		./scripts/gates/tz-matrix.sh --self-test
		local tz
		for tz in Pacific/Kiritimati Pacific/Midway; do
			echo "  TZ=$tz"
			TZ=$tz go test ./... -count=1
		done
		;;
	ci.yml:changelog-scope)
		./scripts/gates/changelog-scope.sh --self-test
		# Гейт хочет диапазон: в CI это база PR, здесь — точка расхождения с main.
		./scripts/gates/changelog-scope.sh "$(git merge-base origin/main HEAD)..HEAD"
		;;
	ci.yml:ci-parity)
		./scripts/gates/ci-parity.sh --self-test
		;;
	ci.yml:arch-map)
		./scripts/gates/arch-map.sh --self-test
		./scripts/gates/arch-map.sh --commit-warn
		;;
	ci.yml:capabilities)
		./scripts/gates/capabilities.sh --self-test
		./scripts/gates/capabilities.sh
		;;
	ci.yml:govulncheck)
		command -v govulncheck >/dev/null || go install golang.org/x/vuln/cmd/govulncheck@v1.1.4
		govulncheck ./...
		;;
	ci.yml:gitleaks)
		gitleaks git --no-banner --redact --exit-code 1 .
		;;
	ci.yml:docker)
		docker build -t kbengine:ci .
		echo "  ⚠️ скан trivy пропущен: trivy не установлен"
		;;
	*)
		echo "run_job: нет команды для $1" >&2
		return 1
		;;
	esac
}

report() { # печатает состав; код 1, если таблица разошлась с диском
	local disk key kind note failed=0
	disk=$(jobs_on_disk)
	local -a local_jobs=() absent_jobs=() na_jobs=() unknown=()
	while read -r key; do
		[[ -z $key ]] && continue
		local entry
		entry=$(lookup "$key")
		if [[ -z $entry ]]; then
			unknown+=("$key")
			continue
		fi
		kind=${entry%%|*}
		note=${entry#*|}
		case $kind in
		local) local_jobs+=("$key|$note") ;;
		absent) absent_jobs+=("$key|$note") ;;
		n/a) na_jobs+=("$key|$note") ;;
		esac
	done <<<"$disk"

	local n_files
	n_files=$(find "$WORKFLOWS" -maxdepth 1 -name '*.yml' | wc -l | tr -d ' ')
	echo "состав CI взят с диска: $WORKFLOWS — файлов $n_files, джоб $(echo "$disk" | grep -c . || true)"

	# Прогон отличается от CI не только составом джоб, но и тем, ЧЕМ он их гонит.
	# CI ставит Go по GO_VERSION с check-latest, то есть свежайший патч; локальная
	# цепочка живёт своей жизнью, и govulncheck здесь краснее не из-за кода.
	local go_local ci_spec
	go_local=$(go version 2>/dev/null | awk '{print $3}')
	ci_spec=$(awk -F'"' '/GO_VERSION:/ {print $2; exit}' "$WORKFLOWS/ci.yml" 2>/dev/null)
	if [[ -n $go_local ]]; then
		echo "инструменты: локально $go_local · CI ставит последний патч ${ci_spec:-?} (check-latest)"
		# Разница в патче — не косметика: 19.08 локальный go1.26.5 давал шесть
		# достижимых уязвимостей stdlib, а CI на свежем патче был зелёным. Пока
		# отставание не названо, оно читается как «у меня что-то краснеет».
		# Спрашиваем go.dev, а не держим список в скрипте: список устареет.
		# Ответ забирается целиком, и только потом разбирается. Конвейер
		# curl | awk с ранним exit валит curl по SIGPIPE, а pipefail делает из
		# этого падение всего гейта — проверено, код 23 при нормальном на вид выводе.
		local body latest
		body=$(curl -sf -m 5 "https://go.dev/dl/?mode=json" 2>/dev/null || true)
		latest=$(printf '%s' "$body" |
			awk -v br="${ci_spec:-}" '
				found { next }
				/"version": "go/ {
					v = $2; gsub(/[",]/, "", v)
					if (br == "" || index(v, "go" br ".") == 1) { print v; found = 1 }
				}' || true)
		if [[ -z $latest ]]; then
			echo "  (свежесть патча не проверена: go.dev не ответил)"
		elif [[ $latest != "$go_local" ]]; then
			echo "  ⚠️ вышел $latest — локальная цепочка отстаёт, govulncheck здесь краснее, чем в CI"
		fi
	fi
	echo
	echo "воспроизводится локально — ${#local_jobs[@]}"
	local row
	for row in "${local_jobs[@]}"; do printf "  %-24s %s\n" "${row%%|*}" "${row#*|}"; done
	echo
	echo "воспроизвести нечем — ${#absent_jobs[@]}"
	for row in "${absent_jobs[@]}"; do printf "  %-24s %s\n" "${row%%|*}" "${row#*|}"; done
	echo
	echo "изменения не проверяют — ${#na_jobs[@]}"
	for row in "${na_jobs[@]}"; do printf "  %-36s %s\n" "${row%%|*}" "${row#*|}"; done

	if ((${#unknown[@]})); then
		echo
		echo "❌ джоба есть в CI, а в таблице ci-parity.sh её нет — ${#unknown[@]}:"
		for row in "${unknown[@]}"; do echo "    $row"; done
		echo "   Допишите строку в TABLE: воспроизводима — с командой в run_job,"
		echo "   иначе видом absent и причиной."
		failed=1
	fi

	# Обратная половина: запись живёт, а джобы уже нет. Без неё локальный прогон
	# годами гонял бы то, чего в CI не существует, и молчал об этом.
	local stale=()
	for row in "${TABLE[@]}"; do
		key=${row%%|*}
		grep -qx "$key" <<<"$disk" || stale+=("$key")
	done
	if ((${#stale[@]})); then
		echo
		echo "❌ в таблице есть запись, а джобы в CI нет — ${#stale[@]}:"
		for row in "${stale[@]}"; do echo "    $row"; done
		failed=1
	fi

	if ((failed == 0 && ${#absent_jobs[@]} > 0)); then
		echo
		echo "⚠️ зелёный локальный прогон НЕ равен зелёному CI:"
		echo "   ${#absent_jobs[@]} джобы здесь не проверялись вовсе."
	fi
	return $failed
}

self_test() {
	local work
	work=$(mktemp -d)
	# shellcheck disable=SC2064
	trap "rm -rf '$work'" RETURN
	cp "$WORKFLOWS"/*.yml "$work"/

	# Подсадка 1: в CI приехала джоба, которой нет в таблице.
	printf '\n  surprise:\n    runs-on: ubuntu-latest\n' >>"$work/ci.yml"
	# Вывод забирается в переменную, а не идёт в grep по конвейеру: при pipefail
	# кодом конвейера станет код падающего гейта, и проверка «назвал ли он джобу»
	# провалится ровно тогда, когда гейт сработал правильно.
	local out
	out=$(CI_PARITY_WORKFLOWS=$work "$0" 2>&1) && {
		echo "self-test: новая джоба в CI не замечена — гейт не работает" >&2
		return 1
	}
	if ! grep -q "surprise" <<<"$out"; then
		echo "self-test: гейт упал, но не назвал джобу surprise" >&2
		return 1
	fi
	echo "  ✓ подсадка 1: новая джоба в CI названа"

	# Подсадка 2: джоба из CI исчезла, запись в таблице осталась.
	cp "$WORKFLOWS"/*.yml "$work"/
	awk '/^  gitleaks:/{skip=1} skip && /^  [a-zA-Z0-9_-]+:[[:space:]]*$/ && !/^  gitleaks:/{skip=0} !skip' \
		"$WORKFLOWS/ci.yml" >"$work/ci.yml"
	out=$(CI_PARITY_WORKFLOWS=$work "$0" 2>&1) && {
		echo "self-test: исчезнувшая джоба не замечена — проверена лишь одна половина" >&2
		return 1
	}
	if ! grep -q "ci.yml:gitleaks" <<<"$out"; then
		echo "self-test: гейт упал, но не назвал исчезнувшую джобу" >&2
		return 1
	fi
	echo "  ✓ подсадка 2: исчезнувшая джоба названа"
	echo "self-test пройден: обе половины сверки работают"
}

case ${1:-} in
--self-test) self_test ;;
--run)
	report
	echo
	echo "── выполняю воспроизводимое ──"
	# Список забирается в массив ДО цикла, а stdin каждой джобы закрыт. Иначе
	# первая же команда, читающая stdin, съедает остаток списка: прогон молча
	# обрывался после govulncheck и возвращал ноль, не тронув gitleaks и docker.
	planned=()
	while read -r key; do
		[[ -z $key ]] && continue
		entry=$(lookup "$key")
		[[ ${entry%%|*} == local ]] && planned+=("$key")
	done < <(jobs_on_disk)

	ran=0
	for key in "${planned[@]}"; do
		echo
		echo "▶ $key"
		run_job "$key" </dev/null
		# Не ((ran++)): постинкремент возвращает СТАРОЕ значение, на нуле это код 1,
		# и set -e убивает прогон после первой же джобы.
		ran=$((ran + 1))
	done

	# Отчёт об успехе обязан совпасть с планом. Прогон, оборвавшийся на середине,
	# и прогон, прошедший целиком, снаружи выглядят одинаково — пока их не сверить.
	if ((ran != ${#planned[@]})); then
		echo
		echo "❌ выполнено $ran из ${#planned[@]} — прогон оборвался, зелёным его считать нельзя"
		exit 1
	fi
	echo
	echo "✓ выполнено $ran из ${#planned[@]} локальных джоб; невоспроизводимые перечислены выше"
	;;
*) report ;;
esac
