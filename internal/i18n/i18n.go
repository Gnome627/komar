// Package i18n holds the UI strings in English and Russian. The language is
// picked from the system locale: Russian when the locale says "ru", English
// otherwise.
package i18n

import (
	"fmt"
	"os"
	"strings"
)

type Lang string

const (
	EN Lang = "en"
	RU Lang = "ru"
)

var current = EN

// Detect picks the language from KOMAR_LANG, then LC_ALL, LC_MESSAGES,
// LANGUAGE and LANG, the same order gettext uses.
func Detect() Lang {
	if v := os.Getenv("KOMAR_LANG"); v != "" {
		return parse(v)
	}
	for _, env := range []string{"LC_ALL", "LC_MESSAGES", "LANGUAGE", "LANG"} {
		if v := os.Getenv(env); v != "" && v != "C" && v != "POSIX" {
			return parse(v)
		}
	}
	return EN
}

func parse(v string) Lang {
	v = strings.ToLower(v)
	// LANGUAGE may hold a priority list like "ru:en".
	first := strings.Split(v, ":")[0]
	if strings.HasPrefix(first, "ru") {
		return RU
	}
	return EN
}

func Set(l Lang)    { current = l }
func Current() Lang { return current }

// T returns the translated string for key, formatted with args when given.
func T(key string, args ...any) string {
	var s string
	var ok bool
	if current == RU {
		s, ok = ru[key]
	}
	if !ok {
		s, ok = en[key]
	}
	if !ok {
		s = key
	}
	if len(args) > 0 {
		return fmt.Sprintf(s, args...)
	}
	return s
}

var en = map[string]string{
	"panel.context":    "Context",
	"panel.namespace":  "Namespace",
	"panel.pods":       "Pods",
	"panel.containers": "Containers",
	"panel.jobs":       "Jobs",
	"panel.related":    "Related",
	"panel.main":       "Main",

	"tab.logs":     "Logs",
	"tab.describe": "Describe",
	"tab.yaml":     "YAML",
	"tab.events":   "Events",
	"tab.rollout":  "Rollout",
	"tab.output":   "Output",

	"ns.all": "all namespaces",

	"status.loading":      "loading…",
	"status.empty":        "nothing here",
	"status.noselection":  "select a resource",
	"status.connecting":   "connecting to %s…",
	"status.unreachable":  "cluster unreachable: %s",
	"status.switched_ctx": "context → %s",
	"status.switched_ns":  "namespace → %s",
	"status.metrics_none": "no metrics",

	"confirm.title":         "Confirm",
	"confirm.delete":        "Delete %s %s?",
	"confirm.force_delete":  "FORCE delete %s %s (grace period 0)?",
	"confirm.delete_many":   "Delete %d objects?",
	"confirm.restart":       "Restart rollout of %s %s?",
	"confirm.undo":          "Roll %s %s back to revision %d?",
	"confirm.yes_no":        "y / enter — yes    n / esc — no",
	"confirm.not_ns_scoped": "This is a cluster-scoped object.",

	"delete.ok":           "%s %s deleted",
	"delete.fail":         "delete failed: %s",
	"scale.title":         "Scale %s",
	"scale.hint":          "↑/↓ or +/- change, digits type, enter apply, esc cancel",
	"scale.ok":            "%s scaled to %d",
	"scale.fail":          "scale failed: %s",
	"scale.unsupported":   "%s can't be scaled",
	"restart.ok":          "rollout restart of %s triggered",
	"restart.fail":        "restart failed: %s",
	"restart.unsupported": "%s has no rollout",
	"undo.ok":             "%s rolled back to revision %d",
	"undo.fail":           "rollback failed: %s",
	"rollout.history":     "History",
	"rollout.progress":    "Progress",
	"rollout.revision":    "rev",
	"rollout.current":     "current",
	"rollout.done":        "rollout complete",
	"rollout.waiting":     "waiting for rollout…",
	"rollout.hint":        "j/k choose revision · enter roll back · r restart",
	"rollout.old":         "old",
	"rollout.new":         "new",
	"rollout.none":        "no revisions",

	"exec.no_pod":         "select a pod to exec into",
	"exec.forbidden":      "Not enough privileges",
	"exec.forbidden_body": "User %s may not %s %s in namespace %s.\n\nkubectl auth can-i %s %s -n %s → no",
	"exec.reason":         "Reason: %s",
	"exec.failed":         "exec finished with error: %s",
	"exec.no_shell":       "No shell in the container. Press b to start an ephemeral debug container.",
	"exec.new_window":     "opened in a new terminal window",
	"exec.pick_container": "Container",
	"debug.image":         "Debug image",
	"debug.forbidden":     "Not enough privileges for ephemeral containers",
	"debug.not_pod":       "debug works on pods only",

	"logs.title":          "logs",
	"logs.search":         "search (regex)",
	"logs.selector":       "label selector",
	"logs.filter_mode":    "filter",
	"logs.highlight_mode": "highlight",
	"logs.previous":       "previous",
	"logs.follow":         "follow",
	"logs.paused":         "paused",
	"logs.since":          "since",
	"logs.matches":        "%d matches",
	"logs.no_target":      "select a pod or a workload to see logs",
	"logs.hint":           "/ search · n/N next/prev · m mode · p previous · t since · f follow · w wrap · L selector",
	"logs.bad_regex":      "bad regex: %s",
	"logs.stream_err":     "log stream %s: %s",

	"events.hint":         "w warnings only · a all namespaces · s sort · o grouping",
	"events.sort_last":    "last seen",
	"events.sort_count":   "count",
	"events.sort_rate":    "rate",
	"events.group_obj":    "by object",
	"events.group_reason": "by reason",
	"events.warn_only":    "warnings",
	"events.all":          "all",
	"events.rate":         "%.1f/min",
	"events.none":         "no events",

	"cmd.prompt":  "kubectl",
	"cmd.running": "running: %s",
	"cmd.exit":    "exit code %d",
	"cmd.hint":    "tab complete · ↑/↓ history · enter run · esc close",

	"kinds.title":   "Resource kinds",
	"picker.filter": "type to filter",

	"filter.prompt": "filter",

	"help.title": "komar — keys",

	"err.forbidden":     "forbidden: %s",
	"err.no_kubeconfig": "no kubeconfig contexts found (KUBECONFIG / ~/.kube/config)",

	"top.cpu":  "cpu",
	"top.mem":  "mem",
	"top.warn": "warn",

	"misc.terminating": "terminating",
	"misc.copied":      "copied",
	"misc.yes":         "yes",
	"misc.no":          "no",
	"misc.any_key":     "any key",
}

var ru = map[string]string{
	"panel.context":    "Контекст",
	"panel.namespace":  "Неймспейс",
	"panel.pods":       "Поды",
	"panel.containers": "Контейнеры",
	"panel.jobs":       "Джобы",
	"panel.related":    "Связанные",
	"panel.main":       "Главная",

	"tab.logs":     "Логи",
	"tab.describe": "Описание",
	"tab.yaml":     "YAML",
	"tab.events":   "События",
	"tab.rollout":  "Роллаут",
	"tab.output":   "Вывод",

	"ns.all": "все неймспейсы",

	"status.loading":      "загрузка…",
	"status.empty":        "пусто",
	"status.noselection":  "выберите ресурс",
	"status.connecting":   "подключение к %s…",
	"status.unreachable":  "кластер недоступен: %s",
	"status.switched_ctx": "контекст → %s",
	"status.switched_ns":  "неймспейс → %s",
	"status.metrics_none": "нет метрик",

	"confirm.title":         "Подтверждение",
	"confirm.delete":        "Удалить %s %s?",
	"confirm.force_delete":  "ПРИНУДИТЕЛЬНО удалить %s %s (grace period 0)?",
	"confirm.delete_many":   "Удалить %d объектов?",
	"confirm.restart":       "Перезапустить роллаут %s %s?",
	"confirm.undo":          "Откатить %s %s на ревизию %d?",
	"confirm.yes_no":        "y / enter — да    n / esc — нет",
	"confirm.not_ns_scoped": "Это объект уровня кластера.",

	"delete.ok":           "%s %s удалён",
	"delete.fail":         "не удалось удалить: %s",
	"scale.title":         "Реплики %s",
	"scale.hint":          "↑/↓ или +/- менять, цифры — ввод, enter применить, esc отмена",
	"scale.ok":            "%s: реплик теперь %d",
	"scale.fail":          "не удалось изменить реплики: %s",
	"scale.unsupported":   "%s нельзя масштабировать",
	"restart.ok":          "роллаут %s запущен",
	"restart.fail":        "не удалось перезапустить: %s",
	"restart.unsupported": "у %s нет роллаута",
	"undo.ok":             "%s откачен на ревизию %d",
	"undo.fail":           "не удалось откатить: %s",
	"rollout.history":     "История",
	"rollout.progress":    "Прогресс",
	"rollout.revision":    "рев",
	"rollout.current":     "текущая",
	"rollout.done":        "роллаут завершён",
	"rollout.waiting":     "ждём роллаут…",
	"rollout.hint":        "j/k выбрать ревизию · enter откатить · r перезапустить",
	"rollout.old":         "старый",
	"rollout.new":         "новый",
	"rollout.none":        "ревизий нет",

	"exec.no_pod":         "выберите под, чтобы зайти в него",
	"exec.forbidden":      "Недостаточно прав",
	"exec.forbidden_body": "Пользователю %s нельзя %s %s в неймспейсе %s.\n\nkubectl auth can-i %s %s -n %s → no",
	"exec.reason":         "Причина: %s",
	"exec.failed":         "exec завершился с ошибкой: %s",
	"exec.no_shell":       "В контейнере нет шелла. Нажмите b, чтобы запустить отладочный ephemeral-контейнер.",
	"exec.new_window":     "открыто в новом окне терминала",
	"exec.pick_container": "Контейнер",
	"debug.image":         "Образ для отладки",
	"debug.forbidden":     "Недостаточно прав на ephemeral-контейнеры",
	"debug.not_pod":       "отладка работает только для подов",

	"logs.title":          "логи",
	"logs.search":         "поиск (regex)",
	"logs.selector":       "label selector",
	"logs.filter_mode":    "фильтр",
	"logs.highlight_mode": "подсветка",
	"logs.previous":       "предыдущий",
	"logs.follow":         "следить",
	"logs.paused":         "пауза",
	"logs.since":          "за",
	"logs.matches":        "совпадений: %d",
	"logs.no_target":      "выберите под или workload, чтобы увидеть логи",
	"logs.hint":           "/ поиск · n/N след/пред · m режим · p previous · t период · f следить · w перенос · L селектор",
	"logs.bad_regex":      "неверный regex: %s",
	"logs.stream_err":     "поток логов %s: %s",

	"events.hint":         "w только warning · a все неймспейсы · s сортировка · o группировка",
	"events.sort_last":    "последние",
	"events.sort_count":   "количество",
	"events.sort_rate":    "частота",
	"events.group_obj":    "по объекту",
	"events.group_reason": "по причине",
	"events.warn_only":    "warning",
	"events.all":          "все",
	"events.rate":         "%.1f/мин",
	"events.none":         "событий нет",

	"cmd.prompt":  "kubectl",
	"cmd.running": "выполняю: %s",
	"cmd.exit":    "код выхода %d",
	"cmd.hint":    "tab дополнить · ↑/↓ история · enter выполнить · esc закрыть",

	"kinds.title":   "Типы ресурсов",
	"picker.filter": "начните печатать для фильтра",

	"filter.prompt": "фильтр",

	"help.title": "komar — клавиши",

	"err.forbidden":     "доступ запрещён: %s",
	"err.no_kubeconfig": "в kubeconfig нет контекстов (KUBECONFIG / ~/.kube/config)",

	"top.cpu":  "cpu",
	"top.mem":  "mem",
	"top.warn": "warn",

	"misc.terminating": "завершается",
	"misc.copied":      "скопировано",
	"misc.yes":         "да",
	"misc.no":          "нет",
	"misc.any_key":     "любая клавиша",
}
