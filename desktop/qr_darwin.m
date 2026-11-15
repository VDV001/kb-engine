//go:build darwin && cgo

// QR привязки телефона (#622): CoreImage рисует, CIDetector читает обратно.
// Только системные фреймворки (решение владельца 15.11.2026).
#import <Foundation/Foundation.h>
#import <CoreImage/CoreImage.h>
#include <stdlib.h>
#include <string.h>

// kbQRPNG: 0 — готово (*out — malloc, освобождает Go), 2 — текст не влез.
int kbQRPNG(const char *text, int n, int scale, void **out, int *outLen) {
	@autoreleasepool {
		CIFilter *f = [CIFilter filterWithName:@"CIQRCodeGenerator"];
		if (f == nil) return 1;
		[f setValue:[NSData dataWithBytes:text length:n] forKey:@"inputMessage"];
		[f setValue:@"M" forKey:@"inputCorrectionLevel"];
		CIImage *img = f.outputImage;
		if (img == nil) return 2;
		// Ближайший сосед: модули остаются резкими квадратами, без размытых краев.
		img = [[img imageBySamplingNearest] imageByApplyingTransform:CGAffineTransformMakeScale(scale, scale)];
		CGColorSpaceRef cs = CGColorSpaceCreateWithName(kCGColorSpaceSRGB);
		NSData *png = [[CIContext contextWithOptions:nil] PNGRepresentationOfImage:img format:kCIFormatRGBA8 colorSpace:cs options:@{}];
		CGColorSpaceRelease(cs);
		if (png == nil) return 3;
		*out = malloc(png.length);
		if (*out == NULL) return 3;
		memcpy(*out, png.bytes, png.length);
		*outLen = (int)png.length;
		return 0;
	}
}

// kbQRDecode: 0 — прочитано (*out — strdup, освобождает Go).
int kbQRDecode(const void *png, int n, char **out) {
	@autoreleasepool {
		CIImage *img = [CIImage imageWithData:[NSData dataWithBytes:png length:n]];
		if (img == nil) return 1;
		CIDetector *d = [CIDetector detectorOfType:CIDetectorTypeQRCode context:nil options:@{CIDetectorAccuracy: CIDetectorAccuracyHigh}];
		for (CIFeature *f in [d featuresInImage:img]) {
			if (![f isKindOfClass:[CIQRCodeFeature class]]) continue;
			NSString *s = ((CIQRCodeFeature *)f).messageString;
			if (s == nil) continue;
			*out = strdup(s.UTF8String);
			return *out == NULL ? 3 : 0;
		}
		return 2;
	}
}
