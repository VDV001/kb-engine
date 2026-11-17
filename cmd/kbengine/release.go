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
	"strings"

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
	}
	fmt.Fprintln(stderr, releaseUsage)
	return 2
}

const releaseUsage = `usage: kbengine release key
       kbengine release sign --version vX.Y.Z --out <dir> darwin-arm64=<file> windows-amd64=<file>
key создает ключ подписи в Связке (один раз) и печатает открытый ключ;
sign пишет <dir>/dvk-manifest.json и .sig и печатает открытый ключ подписи`

type releaseKeyStore interface {
	Get(account string) (string, error)
	Set(account, secret string) error
}

// releaseKeyAccount — учетка ключа в службе «DVK release signing»; секрет —
// seed ed25519 hex-ом.
const releaseKeyAccount = "ed25519"

// releaseKeys is the owner's Связка; тесты подменяют ее, настоящую не трогают.
var releaseKeys = func() releaseKeyStore { return keychain.Store{Service: "DVK release signing"} }

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
	m, err := domain.NewReleaseManifest(*version, assets...)
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

func writeRelease(dir string, manifest, sig []byte) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	name := filepath.Join(dir, "dvk-manifest.json")
	if err := os.WriteFile(name, manifest, 0o644); err != nil {
		return err
	}
	return os.WriteFile(name+".sig", sig, 0o644)
}

func releasePrivateKey() (ed25519.PrivateKey, error) {
	v, err := releaseKeys().Get(releaseKeyAccount)
	if errors.Is(err, keychain.ErrNotFound) {
		return nil, errors.New("в Связке нет ключа подписи — сначала kbengine release key")
	}
	if err != nil {
		return nil, err
	}
	seed, err := hex.DecodeString(strings.TrimSpace(v))
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, errors.New("ключ подписи в Связке поврежден")
	}
	priv := ed25519.NewKeyFromSeed(seed)
	clear(seed)
	return priv, nil
}

var (
	errNoReleaseKey  = errors.New("stub")
	errBadReleaseKey = errors.New("stub2")
)
