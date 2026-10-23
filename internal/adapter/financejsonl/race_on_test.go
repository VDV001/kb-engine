//go:build race

package financejsonl_test

// Под -race порог свой: детектор гонок замедляет каждую операцию, и частное
// у чистого кода и у квадрата получается другим, чем без него.
const scaleLimit = 13.0
