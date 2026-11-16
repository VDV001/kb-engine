// Шифрование чата на телефоне (#622): тот же формат, что mailcrypt в Go —
// одна правда, сверенная тестом phonepage (оба контрольных вектора, Go ↔ JS).
//
//   версия (1) | id ключа (4) | отправитель (8) | счетчик (8, big-endian) | нонс (12) | AES-256-GCM: текст и тег (16)
//   AAD = "kbmail\0" + заголовок (33) + чат + "\0" + сторона
//
// Ключ импортируется неизвлекаемым CryptoKey: после importKey его байтов нет
// ни в странице, ни в хранилище браузера. Счетчик — BigInt (setBigUint64):
// младших 32 бит мало.

const VERSION = 1;
const HEADER = 33;
const OVERHEAD = HEADER + 16;
const MAX_SEALED = 64 << 10;
const enc = new TextEncoder();
// RESERVED — имена, занятые маршрутами ящика (domain.mailReserved).
const RESERVED = new Set(["devices", "push", "notify", "files"]);
const subtle = globalThis.crypto.subtle;

export class CryptoError extends Error {
  constructor(code, message) {
    super(message);
    this.code = code; // format | wrong-key | not-authentic
  }
}

function concat(...parts) {
  const out = new Uint8Array(parts.reduce((n, p) => n + p.length, 0));
  let at = 0;
  for (const p of parts) { out.set(p, at); at += p.length; }
  return out;
}

const b64url = (b) => btoa(String.fromCharCode(...b)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");

// importKey reads the key from the QR (base64url, 43 знака, без «=»).
export async function importKey(text) {
  if (typeof text !== "string" || !/^[A-Za-z0-9_-]{43}$/.test(text)) throw new CryptoError("format", "ключ: не 43 знака base64url");
  const raw = Uint8Array.from(atob(text.replace(/-/g, "+").replace(/_/g, "/")), (ch) => ch.charCodeAt(0));
  if (raw.length !== 32 || b64url(raw) !== text) throw new CryptoError("format", "ключ: не каноническая запись");
  const id = new Uint8Array(await subtle.digest("SHA-256", concat(enc.encode("kbmail key id\0"), raw))).slice(0, 4);
  const key = await subtle.importKey("raw", raw, "AES-GCM", false, ["encrypt", "decrypt"]);
  raw.fill(0);
  return { key, keyId: id };
}

function aad(header, chat, from) {
  return concat(enc.encode("kbmail\0"), header, enc.encode(chat), new Uint8Array([0]), enc.encode(from));
}

// seal encrypts plain (Uint8Array) for chat from the side; nonce — только для тестов.
export async function seal(k, { chat, from, sender, counter }, plain, nonce) {
  if (!/^[a-z0-9-]{1,64}$/.test(chat) || RESERVED.has(chat)) throw new CryptoError("format", "чат: " + chat);
  if (from !== "phone" && from !== "desk") throw new CryptoError("format", "сторона: " + from);
  if (!(sender instanceof Uint8Array) || sender.length !== 8) throw new CryptoError("format", "отправитель: 8 байт");
  if (!plain.length || plain.length > MAX_SEALED - OVERHEAD) throw new CryptoError("format", "текст: пустой или больше предела ящика");
  const iv = nonce ?? globalThis.crypto.getRandomValues(new Uint8Array(12));
  const header = new Uint8Array(HEADER);
  header[0] = VERSION;
  header.set(k.keyId, 1);
  header.set(sender, 5);
  new DataView(header.buffer).setBigUint64(13, BigInt(counter));
  header.set(iv, 21);
  const ct = new Uint8Array(await subtle.encrypt({ name: "AES-GCM", iv, additionalData: aad(header, chat, from) }, k.key, plain));
  return concat(header, ct);
}

// open checks and decrypts a message the server says came to chat from the side.
export async function open(k, chat, from, sealed) {
  if (sealed.length < OVERHEAD || sealed[0] !== VERSION) throw new CryptoError("format", "не запечатанное сообщение");
  const header = sealed.slice(0, HEADER);
  if (!header.slice(1, 5).every((b, i) => b === k.keyId[i])) throw new CryptoError("wrong-key", "закрыто другим ключом");
  let plain;
  try {
    plain = new Uint8Array(await subtle.decrypt({ name: "AES-GCM", iv: header.slice(21, 33), additionalData: aad(header, chat, from) }, k.key, sealed.slice(HEADER)));
  } catch {
    throw new CryptoError("not-authentic", "сообщение правили или оно чужое");
  }
  return { sender: header.slice(5, 13), counter: new DataView(header.buffer).getBigUint64(13), plain };
}

// Кусок файла (#658), формат mailcrypt.SealFile:
//   версия (1) | id ключа (4) | нонс (12) | AES-256-GCM: файл и тег (16)
//   AAD = "kbmail file\0" + заголовок (17): сообщение не откроется как файл.
const FILE_HEADER = 17;
const FILE_OVERHEAD = FILE_HEADER + 16;
const MAX_FILE = 3 << 20; // domain.MaxMailFile: предел запроса Serverless Container
// MAX_FILE_PLAIN — самый длинный кусок файла до шифрования (#650).
export const MAX_FILE_PLAIN = MAX_FILE - 33;

// sealFile encrypts one file piece.
export async function sealFile(k, plain) {
  if (!plain.length || plain.length > MAX_FILE - FILE_OVERHEAD) throw new CryptoError("format", "файл: пустой или больше куска ящика");
  const header = new Uint8Array(FILE_HEADER);
  header[0] = VERSION;
  header.set(k.keyId, 1);
  globalThis.crypto.getRandomValues(header.subarray(5));
  const ct = new Uint8Array(await subtle.encrypt({ name: "AES-GCM", iv: header.slice(5), additionalData: fileAAD(header) }, k.key, plain));
  return concat(header, ct);
}

// openFile checks and decrypts one file piece.
export async function openFile(k, sealed) {
  if (sealed.length < FILE_OVERHEAD || sealed[0] !== VERSION) throw new CryptoError("format", "не запечатанный файл");
  const header = sealed.slice(0, FILE_HEADER);
  if (!header.slice(1, 5).every((b, i) => b === k.keyId[i])) throw new CryptoError("wrong-key", "закрыто другим ключом");
  try {
    return new Uint8Array(await subtle.decrypt({ name: "AES-GCM", iv: header.slice(5), additionalData: fileAAD(header) }, k.key, sealed.slice(FILE_HEADER)));
  } catch {
    throw new CryptoError("not-authentic", "файл правили или он чужой");
  }
}

const fileAAD = (header) => concat(enc.encode("kbmail file\0"), header);
