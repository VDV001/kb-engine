package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/keychain"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/selfupdate"
)

// runRelease is `kbengine release key|sign` (#651): подпись выпуска окна DVK.
// Закрытый ключ живет только в Связке владельца и наружу не выходит; key
// печатает открытый — его вшивают в окно, sign печатает его же, чтобы скрипт
// выпуска сверил подпись с ключом, который окно знает.
func runRelease(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		fmt.Fprintln(stdout, releaseUsage)
		return 0
	}
	if len(args) == 0 {
		fmt.Fprintln(stderr, releaseUsage)
		return 2
	}
	switch args[0] {
	case "key":
		return releaseKey(stdout, stderr)
	case "sign":
		return releaseSign(args[1:], stdout, stderr)
	case "renew":
		return releaseRenew(args[1:], stdout, stderr)
	}
	fmt.Fprintln(stderr, releaseUsage)
	return 2
}

// defaultValidDays — срок годности подписи манифеста; maxValidDays — потолок:
// долгий срок возвращал бы «заморозку» (ревью #679).
const (
	defaultValidDays = 90
	maxValidDays     = 365
)

const releaseUsage = `usage: kbengine release key
       kbengine release sign --version vX.Y.Z --out <dir> darwin-arm64=<file> windows-amd64=<file>
       kbengine release renew --dir <dir> [--valid-days N]
key создает ключ подписи в Связке (один раз) и печатает открытый ключ;
sign пишет <dir>/dvk-manifest.json и .sig и печатает открытый ключ подписи;
renew продлевает срок манифеста в <dir> без пересборки (окно не верит просроченному)`

type releaseKeyStore interface {
	Get(account string) (string, error)
	Set(account, secret string) error
}

// releaseKeyAccount — учетка ключа в службе «DVK release signing»; секрет —
// seed ed25519 hex-ом.
const releaseKeyAccount = "ed25519"

var (
	errNoReleaseKey  = errors.New("в Связке нет ключа подписи — сначала kbengine release key")
	errBadReleaseKey = errors.New("ключ подписи в Связке поврежден")
	// Связка — macOS, Диспетчер учетных данных — Windows; на Linux security нет.
	errReleaseOS = errors.New("подпись выпуска — только на macOS или Windows, где есть хранилище ключа")
)

// releaseKeys is the owner's Связка; тесты подменяют ее, настоящую не трогают.
var releaseKeys = func() releaseKeyStore {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		return noKeyStore{}
	}
	return keychain.Store{Service: "DVK release signing"}
}

// noKeyStore is where there is no keychain: каждый вызов — понятный отказ, а не
// «security: executable file not found» (ревью #672).
type noKeyStore struct{}

func (noKeyStore) Get(string) (string, error) { return "", errReleaseOS }
func (noKeyStore) Set(string, string) error   { return errReleaseOS }

func releaseKey(stdout, stderr io.Writer) int {
	store := releaseKeys()
	switch _, err := store.Get(releaseKeyAccount); {
	case err == nil:
		// Новый ключ молча отрезал бы все окна со старым открытым ключом.
		fmt.Fprintln(stderr, "release key: ключ уже есть в Связке («DVK release signing») — второй не создаю; открытый ключ печатает release sign")
		return 1
	case !errors.Is(err, keychain.ErrNotFound):
		fmt.Fprintln(stderr, "release key:", err)
		return 1
	}
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		fmt.Fprintln(stderr, "release key:", err)
		return 1
	}
	if err := store.Set(releaseKeyAccount, hex.EncodeToString(priv.Seed())); err != nil {
		fmt.Fprintln(stderr, "release key:", err)
		return 1
	}
	fmt.Fprintln(stdout, hex.EncodeToString(pub))
	return 0
}

func releaseSign(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("release sign", flag.ContinueOnError)
	fs.SetOutput(stderr)
	version := fs.String("version", "", "release tag, vX.Y.Z")
	out := fs.String("out", "", "directory for dvk-manifest.json and dvk-manifest.json.sig")
	validDays := fs.Int("valid-days", defaultValidDays, "days the manifest is believed; потом — kbengine release renew")
	if code, stop := parseFlags(fs, args); stop {
		return code
	}
	if *out == "" || fs.NArg() == 0 {
		fmt.Fprintln(stderr, releaseUsage)
		return 2
	}
	assets, code := releaseAssets(fs.Args(), stderr)
	if assets == nil {
		return code
	}
	if *validDays < 1 || *validDays > maxValidDays {
		fmt.Fprintf(stderr, "release sign: --valid-days от 1 до %d\n", maxValidDays)
		return 2
	}
	m, err := domain.NewReleaseManifest(*version, time.Now().AddDate(0, 0, *validDays), assets...)
	if err != nil {
		fmt.Fprintln(stderr, "release sign:", err)
		return 2
	}
	priv, err := releasePrivateKey()
	if err != nil {
		fmt.Fprintln(stderr, "release sign:", err)
		return 1
	}
	manifest := m.JSON()
	sig := selfupdate.SignManifest(priv, manifest)
	if err := writeRelease(*out, manifest, sig); err != nil {
		fmt.Fprintln(stderr, "release sign:", err)
		return 1
	}
	fmt.Fprintln(stdout, hex.EncodeToString(priv.Public().(ed25519.PublicKey)))
	return 0
}

// releaseRenew signs the manifest in dir again with a later expiry: тот же
// выпуск и файлы, без пересборки; без этого окно через срок перестает верить.
func releaseRenew(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("release renew", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", "", "directory with dvk-manifest.json")
	validDays := fs.Int("valid-days", defaultValidDays, "days the manifest is believed")
	if code, stop := parseFlags(fs, args); stop {
		return code
	}
	if *dir == "" || *validDays < 1 || *validDays > maxValidDays {
		fmt.Fprintln(stderr, releaseUsage)
		return 2
	}
	raw, err := os.ReadFile(filepath.Join(*dir, "dvk-manifest.json"))
	if err != nil {
		fmt.Fprintln(stderr, "release renew:", err)
		return 1
	}
	old, err := domain.ParseReleaseManifest(raw)
	if err != nil {
		fmt.Fprintln(stderr, "release renew:", err)
		return 2
	}
	priv, err := releasePrivateKey()
	if err != nil {
		fmt.Fprintln(stderr, "release renew:", err)
		return 1
	}
	manifest := old.Renewed(time.Now().AddDate(0, 0, *validDays)).JSON()
	if err := writeRelease(*dir, manifest, selfupdate.SignManifest(priv, manifest)); err != nil {
		fmt.Fprintln(stderr, "release renew:", err)
		return 1
	}
	fmt.Fprintln(stdout, hex.EncodeToString(priv.Public().(ed25519.PublicKey)))
	return 0
}

// releaseAssets reads the platform=file pairs; nil — выйти с кодом code.
func releaseAssets(pairs []string, stderr io.Writer) ([]domain.ReleaseAsset, int) {
	var assets []domain.ReleaseAsset
	for _, pair := range pairs {
		platform, path, ok := strings.Cut(pair, "=")
		if !ok {
			fmt.Fprintf(stderr, "release sign: %q — нужно платформа=файл\n", pair)
			return nil, 2
		}
		body, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintln(stderr, "release sign:", err)
			return nil, 1
		}
		a, err := domain.NewReleaseAsset(platform, filepath.Base(path), body)
		if err != nil {
			fmt.Fprintln(stderr, "release sign:", err)
			return nil, 2
		}
		assets = append(assets, a)
	}
	return assets, 0
}

// writeRelease writes the manifest and its signature as a pair: обе во
// временные файлы, затем на место. Упавшая подпись не оставит новый манифест
// рядом со старой подписью (ревью #672).
func writeRelease(dir string, manifest, sig []byte) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	name := filepath.Join(dir, "dvk-manifest.json")
	if err := os.WriteFile(name+".new", manifest, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(name+".sig.new", sig, 0o644); err != nil {
		_ = os.Remove(name + ".new")
		return err
	}
	if err := os.Rename(name+".sig.new", name+".sig"); err != nil {
		return err
	}
	return os.Rename(name+".new", name)
}

func releasePrivateKey() (ed25519.PrivateKey, error) {
	v, err := releaseKeys().Get(releaseKeyAccount)
	if errors.Is(err, keychain.ErrNotFound) {
		return nil, errNoReleaseKey
	}
	if err != nil {
		return nil, err
	}
	seed, err := hex.DecodeString(strings.TrimSpace(v))
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, errBadReleaseKey
	}
	return ed25519.NewKeyFromSeed(seed), nil
}
