#!/usr/bin/env bash
# Два описания одного развёртывания живут рядом: манифесты в deploy/k8s и чарт
# в deploy/helm. Такие пары расходятся молча — кто-то правит пробу в одном месте
# и не знает про второе, а заметно это становится в проде.
#
# Гейт сравнивает не текст шаблонов, а ОБЪЕКТЫ, попавшие в кластер: спецификацию
# пода из Deployment, поднятого манифестами, и из Deployment, поднятого чартом.
# Текст у них разный по устройству (шаблоны, имена с именем релиза), а вот образ,
# аргументы, порты, пробы, ресурсы и securityContext обязаны совпадать.
#
# Осознанно НЕ сравниваются:
#   volumes         — имя ConfigMap несёт имя релиза у чарта и не несёт у манифестов
#   metadata.labels — у чарта они свои (helm.sh/chart, app.kubernetes.io/instance)
#   replicas        — задаются на месте применения, а не описанием
#
# Использование:
#   deploy-parity.sh <ns-манифестов>/<имя> <ns-чарта>/<имя>
#   deploy-parity.sh --self-test
set -euo pipefail

# Уборка одна на скрипт. Ловушка на RETURN здесь не годится: в bash она ставится
# не для одной функции, а срабатывает на каждом возврате, и локальная переменная
# соседней функции к этому моменту уже не существует.
WORK=""
cleanup() { [[ -n $WORK ]] && rm -rf "$WORK"; }
trap cleanup EXIT

# Существенная часть спецификации пода. jq -S сортирует ключи, иначе диффом
# полез бы порядок полей, к делу не относящийся.
extract() {
	jq -S '.spec.template.spec | {
		securityContext,
		containers: [.containers[] | {
			image, args, ports, livenessProbe, readinessProbe, resources, securityContext,
			volumeMounts: [.volumeMounts[] | {mountPath, readOnly}]
		}]
	}'
}

compare() {
	local left_name=$1 left_file=$2 right_name=$3 right_file=$4
	# Заголовки диффа ставит сам diff. Подменять их потом через sed нельзя:
	# на BSD sed кириллица в правой части подстановки роняет команду, и гейт
	# сообщал бы о расхождении, не называя его.
	if diff -u --label "$left_name" --label "$right_name" "$left_file" "$right_file"; then
		echo "✓ $left_name и $right_name описывают один и тот же под"
		return 0
	fi
	echo
	echo "  Правка дошла до одного описания и не дошла до второго."
	echo "  Чинить в обоих: deploy/k8s/base/deployment.yaml и"
	echo "  deploy/helm/kbengine/templates/deployment.yaml."
	return 1
}

# Проверка проверки: гейт, который никогда не падал, от гейта, который не
# работает, снаружи неотличим. Подсаживаются два расхождения — изменённая проба
# и убранный securityContext, — и каждое обязано быть названо.
self_test() {
	local dir
	dir=$(mktemp -d)
	WORK=$dir

	cat > "$dir/base.json" <<'JSON'
{"spec":{"template":{"spec":{"securityContext":{"runAsNonRoot":true},"containers":[
{"image":"kbengine:ci","args":["serve"],"ports":[{"containerPort":8080}],
"livenessProbe":{"httpGet":{"path":"/healthz"}},"readinessProbe":{"httpGet":{"path":"/readyz"}},
"resources":{"requests":{"cpu":"50m"}},"securityContext":{"readOnlyRootFilesystem":true},
"volumeMounts":[{"mountPath":"/data","readOnly":true}]}]}}}}
JSON

	# 1. Идентичные описания обязаны пройти.
	extract < "$dir/base.json" > "$dir/a.json"
	cp "$dir/a.json" "$dir/b.json"
	if ! compare "слева" "$dir/a.json" "справа" "$dir/b.json" > /dev/null; then
		echo "самопроверка: гейт ругается на два одинаковых описания" >&2
		return 1
	fi

	# 2. Проба указывает на другой путь — классическое «поправил в одном месте».
	sed 's|/readyz|/healthz|' "$dir/base.json" | extract > "$dir/c.json"
	if compare "слева" "$dir/a.json" "справа" "$dir/c.json" > "$dir/out1" 2>&1; then
		echo "самопроверка: расхождение в пробе не поймано" >&2
		return 1
	fi
	grep -q "readyz" "$dir/out1" || {
		echo "самопроверка: гейт упал, но не назвал расхождение в пробе" >&2
		return 1
	}

	# 3. Пропал securityContext контейнера — то, что молча ослабляет безопасность.
	jq 'del(.spec.template.spec.containers[0].securityContext)' "$dir/base.json" | extract > "$dir/d.json"
	if compare "слева" "$dir/a.json" "справа" "$dir/d.json" > "$dir/out2" 2>&1; then
		echo "самопроверка: пропавший securityContext не пойман" >&2
		return 1
	fi

	echo "✓ самопроверка: одинаковые проходят, оба подсаженных расхождения названы"
}

main() {
	if [[ ${1:-} == "--self-test" ]]; then
		self_test
		return
	fi
	if [[ $# -ne 2 ]]; then
		echo "usage: $0 <ns>/<deployment> <ns>/<deployment> | --self-test" >&2
		return 2
	fi

	local dir
	dir=$(mktemp -d)
	WORK=$dir

	local left=$1 right=$2
	kubectl get deployment "${left#*/}" -n "${left%%/*}" -o json | extract > "$dir/left.json"
	kubectl get deployment "${right#*/}" -n "${right%%/*}" -o json | extract > "$dir/right.json"
	compare "манифесты ($left)" "$dir/left.json" "чарт ($right)" "$dir/right.json"
}

main "$@"
