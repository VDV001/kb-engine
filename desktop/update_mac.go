package main

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// maxBundle — предел распакованной сборки окна: Go-бинарь с рамкой весит
// десятки МБ, архив-бомба до сотен ГБ — не наш выпуск.
const maxBundle = 300 << 20

// macBinary — исполняемый файл внутри бандла (justfile: Contents/MacOS/kb).
const macBinary = "Contents/MacOS/kb"

// extractZip unpacks zipPath into dir, строго (#651): пути только внутри dir
// (zip-slip и абсолютные — отказ), ссылки не берутся, общий размер не больше
// limit; исполняемый бит сохраняется. Все записи проверяются до первой записи на
// диск; ошибка при записи стирает уже созданное.
func extractZip(zipPath, dir string, limit int64) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	var total uint64
	for _, f := range r.File {
		if !filepath.IsLocal(f.Name) {
			return fmt.Errorf("архив: путь вне папки: %q", f.Name)
		}
		if m := f.Mode(); !m.IsRegular() && !m.IsDir() {
			return fmt.Errorf("архив: ссылки и устройства не берутся: %q", f.Name)
		}
		total += f.UncompressedSize64
	}
	if total > uint64(limit) {
		return errors.New("архив: распакованное больше предела")
	}
	var made []string
	fail := func(err error) error {
		for _, p := range made {
			_ = os.RemoveAll(p)
		}
		return err
	}
	var written int64
	for _, f := range r.File {
		dst := filepath.Join(dir, f.Name)
		if top := strings.SplitN(filepath.ToSlash(f.Name), "/", 2)[0]; !contains(made, filepath.Join(dir, top)) {
			made = append(made, filepath.Join(dir, top))
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return fail(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return fail(err)
		}
		perm := os.FileMode(0o644)
		if f.Mode()&0o111 != 0 {
			perm = 0o755
		}
		n, err := copyEntry(f, dst, perm, limit-written)
		written += n
		if err != nil {
			return fail(err)
		}
	}
	return nil
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// copyEntry writes one archive file; больше max байт — отказ (заголовок мог соврать).
func copyEntry(f *zip.File, dst string, perm os.FileMode, max int64) (int64, error) {
	in, err := f.Open()
	if err != nil {
		return 0, err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(out, io.LimitReader(in, max+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > max {
		err = errors.New("архив: распакованное больше предела")
	}
	return n, err
}

// installMac replaces the window bundle with the one in zipPath (#651): новый
// собирается рядом (одна файловая система — rename атомарен), прежний уходит в
// <бандл>.old, при сбое возвращается на место. Окно запущенным не трогается:
// macOS держит бинарь открытым, перезапуск — отдельным шагом.
func installMac(zipPath, bundle string) error {
	tmp, err := os.MkdirTemp(filepath.Dir(bundle), ".dvk-new-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	if err := extractZip(zipPath, tmp, maxBundle); err != nil {
		return err
	}
	apps, _ := filepath.Glob(filepath.Join(tmp, "*.app"))
	if len(apps) != 1 {
		return errors.New("в архиве должен быть один бандл .app")
	}
	bin, err := os.Stat(filepath.Join(apps[0], macBinary))
	if err != nil || !bin.Mode().IsRegular() || bin.Mode()&0o111 == 0 {
		return errors.New("в сборке нет исполняемого файла окна")
	}
	old := bundle + ".old"
	if err := os.RemoveAll(old); err != nil {
		return err
	}
	if err := os.Rename(bundle, old); err != nil {
		return err
	}
	if err := os.Rename(apps[0], bundle); err != nil {
		if rerr := os.Rename(old, bundle); rerr != nil {
			return errors.Join(err, rerr)
		}
		return err
	}
	return nil
}

// bundleOf is the .app bundle the executable runs from; не бандл — менять нечего.
func bundleOf(exe string) (string, bool) {
	macos := filepath.Dir(exe)
	contents := filepath.Dir(macos)
	app := filepath.Dir(contents)
	if filepath.Base(macos) != "MacOS" || filepath.Base(contents) != "Contents" || !strings.HasSuffix(app, ".app") {
		return "", false
	}
	return app, true
}
