package main

// explorerSelect is the command line of «Показать в Проводнике»: путь в
// кавычках после запятой. Целиком в кавычках ("/select,C:\a b\f.pdf"), как
// экранирует exec, Проводник аргумент не узнает и открывает «Документы» (ревью #637).
func explorerSelect(path string) string {
	return `explorer "/select,` + path + `"`
}
