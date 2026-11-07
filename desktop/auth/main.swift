// kb-auth check "причина" | kb-auth copy [--hold 30] — Touch ID и буфер для
// экрана «Доступы» окна kb-desktop (#494). Только фреймворки macOS
// (LocalAuthentication, AppKit): ставить ничего не нужно.
//
// check: спрашивает Touch ID (нет его — пароль Мака) с причиной на экране.
//   Код выхода: 0 — человек подтвердил; 1 — отказ или отмена; 2 — проверить
//   нечем (нет пароля у учетной записи). Причина отказа — строкой в stderr.
// copy: читает пароль из stdin и кладет в буфер только этого Мака
//   (.currentHostOnly — без Universal Clipboard) с пометками
//   org.nspasteboard.ConcealedType и TransientType: менеджеры буфера (Maccy,
//   Raycast, Paste) его не сохраняют. Печатает "copied" и ждет --hold секунд,
//   затем стирает буфер, если в нем ничего не меняли (changeCount тот же):
//   человек успел скопировать другое — его не трогаем.
//   Окно не ждет конца: читает "copied" и отпускает процесс.
import AppKit
import Foundation
import LocalAuthentication

setvbuf(stdout, nil, _IOLBF, 0)

func fail(_ msg: String, _ code: Int32) -> Never {
    FileHandle.standardError.write((msg + "\n").data(using: .utf8)!)
    exit(code)
}

let args = Array(CommandLine.arguments.dropFirst())

func check(_ reason: String) -> Never {
    let ctx = LAContext()
    var err: NSError?
    guard ctx.canEvaluatePolicy(.deviceOwnerAuthentication, error: &err) else {
        fail(err?.localizedDescription ?? "Touch ID и пароль Мака недоступны", 2)
    }
    let done = DispatchSemaphore(value: 0)
    var ok = false
    var why = ""
    ctx.evaluatePolicy(.deviceOwnerAuthentication, localizedReason: reason) { success, e in
        ok = success
        why = e?.localizedDescription ?? "отменено"
        done.signal()
    }
    done.wait()
    if ok {
        print("ok")
        exit(0)
    }
    fail(why, 1)
}

func copy(hold: Double) -> Never {
    let data = FileHandle.standardInput.readDataToEndOfFile()
    guard let secret = String(data: data, encoding: .utf8), !secret.isEmpty else {
        fail("пароль не пришел", 2)
    }
    let pb = NSPasteboard.general
    pb.prepareForNewContents(with: .currentHostOnly)
    let item = NSPasteboardItem()
    item.setString(secret, forType: .string)
    item.setString("", forType: NSPasteboard.PasteboardType("org.nspasteboard.ConcealedType"))
    item.setString("", forType: NSPasteboard.PasteboardType("org.nspasteboard.TransientType"))
    guard pb.writeObjects([item]) else { fail("буфер не принял пароль", 1) }
    let mine = pb.changeCount
    print("copied")
    Thread.sleep(forTimeInterval: hold)
    if pb.changeCount == mine {
        pb.clearContents()
    }
    exit(0)
}

switch args.first {
case "check":
    check(args.count > 1 ? args[1] : "открыть доступы")
case "copy":
    var hold = 30.0
    if let i = args.firstIndex(of: "--hold"), i + 1 < args.count, let v = Double(args[i + 1]), v > 0, v <= 600 {
        hold = v
    }
    copy(hold: hold)
default:
    fail("usage: kb-auth check \"причина\" | kb-auth copy [--hold секунд]", 2)
}
