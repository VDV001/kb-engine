# Текст картинки или PDF для kb-extract.exe (#622, этап 5): только то, что есть
# в Windows 10/11 — Windows.Data.Pdf рисует страницы, Windows.Media.Ocr читает.
# Путь и вид файла — переменными KB_FILE и KB_EXT, а не текстом скрипта.
# Выход: 0 — текст в stdout (может быть пустым), 2 — файл не открылся, 3 — нет OCR.
$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [Text.Encoding]::UTF8
Add-Type -AssemblyName System.Runtime.WindowsRuntime
$ext = [System.WindowsRuntimeSystemExtensions].GetMethods() | Where-Object { $_.Name -eq 'AsTask' -and $_.GetParameters().Count -eq 1 }
$asOp = ($ext | Where-Object { $_.GetParameters()[0].ParameterType.Name -eq 'IAsyncOperation`1' })[0]
$asAct = ($ext | Where-Object { $_.GetParameters()[0].ParameterType.Name -eq 'IAsyncAction' })[0]
function Await($op, $t) { $task = $asOp.MakeGenericMethod($t).Invoke($null, @($op)); $task.Wait(-1) | Out-Null; $task.Result }
function AwaitAction($op) { $task = $asAct.Invoke($null, @($op)); $task.Wait(-1) | Out-Null }
$null = [Windows.Storage.StorageFile, Windows.Storage, ContentType = WindowsRuntime]
$null = [Windows.Storage.Streams.InMemoryRandomAccessStream, Windows.Storage.Streams, ContentType = WindowsRuntime]
$null = [Windows.Graphics.Imaging.BitmapDecoder, Windows.Graphics, ContentType = WindowsRuntime]
$null = [Windows.Media.Ocr.OcrEngine, Windows.Foundation, ContentType = WindowsRuntime]
$null = [Windows.Data.Pdf.PdfDocument, Windows.Data.Pdf, ContentType = WindowsRuntime]
$null = [Windows.Globalization.Language, Windows.Globalization, ContentType = WindowsRuntime]

# Русский OCR, если пакет языка стоит; иначе — языки профиля пользователя.
$ocr = [Windows.Media.Ocr.OcrEngine]::TryCreateFromLanguage([Windows.Globalization.Language]::new('ru'))
if (-not $ocr) { $ocr = [Windows.Media.Ocr.OcrEngine]::TryCreateFromUserProfileLanguages() }
if (-not $ocr) { [Console]::Error.WriteLine('в Windows нет движка распознавания текста'); exit 3 }
$max = [Windows.Media.Ocr.OcrEngine]::MaxImageDimension

function Read-Stream($stream) {
    $dec = Await ([Windows.Graphics.Imaging.BitmapDecoder]::CreateAsync($stream)) ([Windows.Graphics.Imaging.BitmapDecoder])
    $t = [Windows.Graphics.Imaging.BitmapTransform]::new()
    $k = [Math]::Min(1.0, [Math]::Min($max / $dec.OrientedPixelWidth, $max / $dec.OrientedPixelHeight))
    $t.ScaledWidth = [uint32][Math]::Floor($dec.PixelWidth * $k)
    $t.ScaledHeight = [uint32][Math]::Floor($dec.PixelHeight * $k)
    $bmp = Await ($dec.GetSoftwareBitmapAsync([Windows.Graphics.Imaging.BitmapPixelFormat]::Bgra8, [Windows.Graphics.Imaging.BitmapAlphaMode]::Premultiplied, $t, [Windows.Graphics.Imaging.ExifOrientationMode]::RespectExifOrientation, [Windows.Graphics.Imaging.ColorManagementMode]::DoNotColorManage)) ([Windows.Graphics.Imaging.SoftwareBitmap])
    $r = Await ($ocr.RecognizeAsync($bmp)) ([Windows.Media.Ocr.OcrResult])
    ($r.Lines | ForEach-Object { $_.Text }) -join "`n"
}

try {
    $file = Await ([Windows.Storage.StorageFile]::GetFileFromPathAsync($env:KB_FILE)) ([Windows.Storage.StorageFile])
    if ($env:KB_EXT -eq 'pdf') {
        $doc = Await ([Windows.Data.Pdf.PdfDocument]::LoadFromFileAsync($file)) ([Windows.Data.Pdf.PdfDocument])
    } else {
        $img = Await ($file.OpenAsync([Windows.Storage.FileAccessMode]::Read)) ([Windows.Storage.Streams.IRandomAccessStream])
    }
} catch {
    [Console]::Error.WriteLine("файл не открылся: $($_.Exception.Message)"); exit 2
}
if ($img) { Read-Stream $img; exit 0 }

# Страницы PDF — картинкой в размер, который OCR принимает; не больше 30, как на Маке.
$pages = [Math]::Min($doc.PageCount, 30)
for ($i = 0; $i -lt $pages; $i++) {
    $page = $doc.GetPage($i)
    $k = [Math]::Min(($max - 100) / $page.Size.Width, ($max - 100) / $page.Size.Height)
    # Не больше 200 dpi (Size — в DIP, 96 на дюйм): этого OCR хватает, а в
    # предел OCR лист A4 — ~7650×9900, около 300 МБ на страницу (ревью #637).
    $k = [Math]::Min($k, 200 / 96)
    $o = [Windows.Data.Pdf.PdfPageRenderOptions]::new()
    $o.DestinationWidth = [uint32][Math]::Floor($page.Size.Width * $k)
    $o.DestinationHeight = [uint32][Math]::Floor($page.Size.Height * $k)
    $ms = [Windows.Storage.Streams.InMemoryRandomAccessStream]::new()
    AwaitAction ($page.RenderToStreamAsync($ms, $o))
    Read-Stream $ms
    $page.Dispose()
}
exit 0
