// kb-listen [--lang ru-RU] [--pause 1.2] [--check] [--record out.wav] — диктовка для окна kb-desktop: слушает
// микрофон и печатает текст по мере речи строками JSON. Только фреймворки
// macOS (Speech, AVFoundation): ставить ничего не нужно.
//
// Протокол (stdout, строка — событие):
//   {"ready":true,"onDevice":true}      микрофон открыт; onDevice — распознавание без сети
//   {"text":"…"}                         текст сказанного на сейчас (целиком, не прирост)
//   {"text":"…","final":true}            итог; после него помощник выходит
//   {"error":"…"}                        отказ словами человека; затем выход с кодом ≠ 0
// Стоп — закрытый stdin: помощник дослушивает и отдает итог. Звук никуда не пишется.
// --pause N — разговор без рук: тишина N секунд после речи тоже стоп.
// --record PATH — не распознавать, а записать речь в WAV 16 кГц моно (для
//   сервера распознавания: whisper и т. п.); итог — {"recorded":PATH,"final":true}.
// --check печатает {"auth":"…","onDevice":…,"available":…} без запроса разрешений.
import AVFoundation
import Foundation
import Speech

setvbuf(stdout, nil, _IOLBF, 0)

func emit(_ d: [String: Any]) {
    if let j = try? JSONSerialization.data(withJSONObject: d), let s = String(data: j, encoding: .utf8) {
        print(s)
    }
}

func fail(_ msg: String, _ code: Int32) -> Never {
    emit(["error": msg])
    exit(code)
}

let args = CommandLine.arguments
let lang = args.firstIndex(of: "--lang").flatMap { $0 + 1 < args.count ? args[$0 + 1] : nil } ?? "ru-RU"
guard let recognizer = SFSpeechRecognizer(locale: Locale(identifier: lang)) else {
    fail("распознавание речи не знает язык \(lang)", 2)
}

if args.contains("--check") {
    let auth: String
    switch SFSpeechRecognizer.authorizationStatus() {
    case .authorized: auth = "authorized"
    case .denied: auth = "denied"
    case .restricted: auth = "restricted"
    default: auth = "notDetermined"
    }
    emit(["auth": auth, "onDevice": recognizer.supportsOnDeviceRecognition, "available": recognizer.isAvailable])
    exit(0)
}

let pause = args.firstIndex(of: "--pause").flatMap { $0 + 1 < args.count ? Double(args[$0 + 1]) : nil } ?? 0

let engine = AVAudioEngine()
var stopped = false
var silence: DispatchWorkItem?

// finish stops listening and waits for the final text; итог не пришел за 4 с — выход.
func finish() {
    guard !stopped else { return }
    stopped = true
    engine.stop()
    engine.inputNode.removeTap(onBus: 0)
    request.endAudio()
    DispatchQueue.main.asyncAfter(deadline: .now() + 4) { emit(["final": true]); exit(0) }
}
let request = SFSpeechAudioBufferRecognitionRequest()
var task: SFSpeechRecognitionTask?

func start() {
    request.shouldReportPartialResults = true
    // На устройстве — если macOS умеет этот язык без сети: тогда речь не уходит в Apple.
    if recognizer.supportsOnDeviceRecognition { request.requiresOnDeviceRecognition = true }
    if #available(macOS 13, *) { request.addsPunctuation = true }
    let input = engine.inputNode
    input.installTap(onBus: 0, bufferSize: 1024, format: input.outputFormat(forBus: 0)) { buf, _ in
        request.append(buf)
    }
    engine.prepare()
    do { try engine.start() } catch { fail("микрофон не открылся: \(error.localizedDescription)", 4) }
    emit(["ready": true, "onDevice": recognizer.supportsOnDeviceRecognition])
    task = recognizer.recognitionTask(with: request) { result, error in
        if let r = result {
            emit(["text": r.bestTranscription.formattedString, "final": r.isFinal])
            if r.isFinal { exit(0) }
            if pause > 0 {
                // Речь идет — тишина считается заново.
                DispatchQueue.main.async {
                    silence?.cancel()
                    let item = DispatchWorkItem { finish() }
                    silence = item
                    DispatchQueue.main.asyncAfter(deadline: .now() + pause, execute: item)
                }
            }
        }
        if let e = error as NSError? {
            // Тишина без единого слова — не ошибка: итог пустой.
            if e.domain == "kAFAssistantErrorDomain" && (e.code == 1110 || e.code == 216) {
                emit(["text": "", "final": true]); exit(0)
            }
            fail("распознавание остановилось: \(e.localizedDescription)", 5)
        }
    }
    // Стоп — закрытый stdin.
    DispatchQueue.global().async {
        while readLine() != nil {}
        DispatchQueue.main.async { finish() }
    }
}

// --- Запись для сервера распознавания (--record) -----------------------------------

// Файл записи — глобально: перед выходом обнуляется, и AVAudioFile дописывает
// заголовок WAV (exit сам объекты не освобождает).
var recFile: AVAudioFile?

let recordPath = args.firstIndex(of: "--record").flatMap { $0 + 1 < args.count ? args[$0 + 1] : nil }

func record(to path: String) {
    let input = engine.inputNode
    let inFormat = input.outputFormat(forBus: 0)
    guard let outFormat = AVAudioFormat(commonFormat: .pcmFormatInt16, sampleRate: 16000, channels: 1, interleaved: true),
          let converter = AVAudioConverter(from: inFormat, to: outFormat)
    else { fail("запись не настроилась: формат микрофона", 4) }
    do {
        recFile = try AVAudioFile(forWriting: URL(fileURLWithPath: path), settings: outFormat.settings,
                               commonFormat: .pcmFormatInt16, interleaved: true)
    } catch { fail("файл записи не создался: \(error.localizedDescription)", 4) }
    var spoke = false
    var quietSince = Date()
    // Тише этого уровня — тишина (RMS отсчетов float32 микрофона).
    let silenceLevel: Float = 0.012
    input.installTap(onBus: 0, bufferSize: 4096, format: inFormat) { buf, _ in
        if let ch = buf.floatChannelData?[0] {
            var sum: Float = 0
            for i in 0..<Int(buf.frameLength) { sum += ch[i] * ch[i] }
            let rms = (sum / Float(max(buf.frameLength, 1))).squareRoot()
            if rms > silenceLevel { spoke = true; quietSince = Date() }
            if pause > 0 && spoke && Date().timeIntervalSince(quietSince) > pause {
                DispatchQueue.main.async { finishRecording(path) }
            }
        }
        let cap = AVAudioFrameCount(Double(buf.frameLength) * 16000 / inFormat.sampleRate) + 16
        guard let out = AVAudioPCMBuffer(pcmFormat: outFormat, frameCapacity: cap) else { return }
        var fed = false
        var err: NSError?
        converter.convert(to: out, error: &err) { _, status in
            if fed { status.pointee = .noDataNow; return nil }
            fed = true
            status.pointee = .haveData
            return buf
        }
        if out.frameLength > 0 { try? recFile?.write(from: out) }
    }
    engine.prepare()
    do { try engine.start() } catch { fail("микрофон не открылся: \(error.localizedDescription)", 4) }
    emit(["ready": true, "onDevice": false])
    DispatchQueue.global().async {
        while readLine() != nil {}
        DispatchQueue.main.async { finishRecording(path) }
    }
}

func finishRecording(_ path: String) {
    guard !stopped else { return }
    stopped = true
    engine.stop()
    engine.inputNode.removeTap(onBus: 0)
    recFile = nil // закрыть файл: заголовок WAV дописывается здесь
    emit(["recorded": path, "final": true])
    exit(0)
}

if let path = recordPath {
    AVCaptureDevice.requestAccess(for: .audio) { ok in
        guard ok else {
            fail("нет доступа к микрофону: Системные настройки → Конфиденциальность и безопасность → Микрофон → DVK", 3)
        }
        DispatchQueue.main.async { record(to: path) }
    }
    dispatchMain()
}

SFSpeechRecognizer.requestAuthorization { status in
    guard status == .authorized else {
        fail("нет разрешения на распознавание речи: Системные настройки → Конфиденциальность и безопасность → Распознавание речи → DVK", 3)
    }
    AVCaptureDevice.requestAccess(for: .audio) { ok in
        guard ok else {
            fail("нет доступа к микрофону: Системные настройки → Конфиденциальность и безопасность → Микрофон → DVK", 3)
        }
        DispatchQueue.main.async { start() }
    }
}
dispatchMain()
