// Шифрование чата на телефоне (#622). Заготовка без поведения.
export async function importKey() { return { keyId: new Uint8Array(4) }; }
export async function seal() { return new Uint8Array(0); }
export async function open() { return { sender: new Uint8Array(8), counter: 0n, plain: new Uint8Array(0) }; }
