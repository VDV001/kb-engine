// kb-extract <файл> — текст PDF или картинки в stdout, для поиска по файлам
// окна kb-desktop. Только фреймворки macOS (PDFKit, Vision, ImageIO): ставить
// ничего не нужно, сторонних программ нет (решение владельца 03.11.2026).
//
// PDF с текстовым слоем отдает его как есть; PDF без текста (скан) и картинки
// распознаются Vision на русском и английском. Коды выхода: 0 — текст в stdout
// (возможно пустой: на картинке нет надписей), 2 — файл не открылся.
import Foundation
import PDFKit
import Vision

// maxOCRPages — сколько страниц скана распознается: толстый скан не держит
// проход окна, а начало документа обычно и есть то, что ищут.
let maxOCRPages = 30
// minTextPerPage — меньше символов на страницу значит, что текстового слоя
// нет (скан с номером страницы или колонтитулом).
let minTextPerPage = 20

func ocr(_ image: CGImage) throws -> String {
    let req = VNRecognizeTextRequest()
    req.recognitionLevel = .accurate
    req.recognitionLanguages = ["ru-RU", "en-US"]
    req.usesLanguageCorrection = true
    try VNImageRequestHandler(cgImage: image).perform([req])
    return (req.results ?? []).compactMap { $0.topCandidates(1).first?.string }.joined(separator: "\n")
}

// render draws a PDF page at twice its size: мелкий шрифт скана Vision на
// исходном масштабе читает с ошибками.
func render(_ page: PDFPage) -> CGImage? {
    let box = page.bounds(for: .mediaBox)
    let scale: CGFloat = 2
    let w = Int(box.width * scale), h = Int(box.height * scale)
    guard w > 0, h > 0,
          let ctx = CGContext(data: nil, width: w, height: h, bitsPerComponent: 8, bytesPerRow: 0,
                              space: CGColorSpaceCreateDeviceRGB(), bitmapInfo: CGImageAlphaInfo.noneSkipLast.rawValue)
    else { return nil }
    ctx.setFillColor(CGColor(red: 1, green: 1, blue: 1, alpha: 1))
    ctx.fill(CGRect(x: 0, y: 0, width: w, height: h))
    ctx.scaleBy(x: scale, y: scale)
    page.draw(with: .mediaBox, to: ctx)
    return ctx.makeImage()
}

func pdfText(_ url: URL) throws -> String? {
    guard let doc = PDFDocument(url: url), !doc.isLocked else { return nil }
    let text = doc.string ?? ""
    if text.trimmingCharacters(in: .whitespacesAndNewlines).count >= minTextPerPage * max(doc.pageCount, 1) {
        return text
    }
    var pages: [String] = []
    for i in 0..<min(doc.pageCount, maxOCRPages) {
        if let page = doc.page(at: i), let image = render(page) {
            pages.append(try ocr(image))
        }
    }
    return pages.joined(separator: "\n\u{0C}")
}

func imageText(_ url: URL) throws -> String? {
    guard let src = CGImageSourceCreateWithURL(url as CFURL, nil),
          let image = CGImageSourceCreateImageAtIndex(src, 0, nil) else { return nil }
    return try ocr(image)
}

guard CommandLine.arguments.count == 2 else {
    FileHandle.standardError.write("usage: kb-extract <file>\n".data(using: .utf8)!)
    exit(64)
}
let url = URL(fileURLWithPath: CommandLine.arguments[1])
do {
    let text = url.pathExtension.lowercased() == "pdf" ? try pdfText(url) : try imageText(url)
    guard let text else { exit(2) }
    FileHandle.standardOutput.write(text.data(using: .utf8)!)
} catch {
    FileHandle.standardError.write("kb-extract: \(error)\n".data(using: .utf8)!)
    exit(1)
}
