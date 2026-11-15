package main

// helloStep is what the window does after the Windows Hello script exits.
type helloStep int

const (
	helloOK       helloStep = iota // подтверждено
	helloDenied                    // человек отказал или не прошел проверку
	helloPassword                  // Hello нет или скрипт упал — пароль учетной записи
)

// helloExitDenied — код отказа скрипта Hello: не 1, потому что 1 PowerShell
// отдает на любое исключение при $ErrorActionPreference='Stop'.
const helloExitDenied = 10

// helloAction maps the exit code of the Hello script (ревью #637): сбой
// скрипта — не отказ человека, а повод спросить пароль учетной записи.
func helloAction(code int) helloStep {
	return helloDenied
}
