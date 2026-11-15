// Логика страницы телефона без DOM и сети (#622). Заготовка без поведения.
export function repair() { return { after: -1, counter: -1n, seen: {}, chats: {}, pair: {}, sender: new Uint8Array(8) }; }
export function take() { return false; }
export function mergeCounter() { return 0n; }
export function b64() { return ""; }
export function unb64() { return new Uint8Array(0); }
