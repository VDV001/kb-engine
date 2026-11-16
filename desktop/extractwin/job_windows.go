package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// dieTogether puts this helper in a job that ends with it: окно снимает
// kb-extract по сроку (CommandContext) — без задания powershell.exe жил бы
// дальше и ел память на огромном PDF (ревью #637). Потомки входят в задание
// сами; не вышло — помощник работает, как раньше.
func dieTogether() {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return
	}
	if err := windows.AssignProcessToJobObject(job, windows.CurrentProcess()); err != nil {
		_ = windows.CloseHandle(job)
	}
	// Описатель не закрывается: он закроется вместе с процессом и снимет задание.
}
