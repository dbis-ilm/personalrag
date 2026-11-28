//go:build darwin && cgo

// Page rendering with CoreGraphics and text recognition with Apple Vision.
// Both run headless and need no permission prompt.

#import <Foundation/Foundation.h>
#import <CoreGraphics/CoreGraphics.h>
#import <Vision/Vision.h>
#include <stdlib.h>
#include <string.h>

// Render at about 216 dpi, capped so huge pages stay within memory.
static const double kScale = 3.0;
static const double kMaxSide = 4000.0;

static char *dupString(NSString *s) { return strdup([s UTF8String]); }

int prag_pdf_pages(const char *path) {
    @autoreleasepool {
        NSURL *url = [NSURL fileURLWithPath:[NSString stringWithUTF8String:path]];
        CGPDFDocumentRef doc = CGPDFDocumentCreateWithURL((__bridge CFURLRef)url);
        if (doc == NULL) return -1;
        int n = (int)CGPDFDocumentGetNumberOfPages(doc);
        CGPDFDocumentRelease(doc);
        return n;
    }
}

// prag_ocr_page returns the page's lines as JSON ([{"t","x","y","h"}], y from
// the top, fractions of the page) in Vision's reading order, or NULL with
// *err set. The caller frees both.
char *prag_ocr_page(const char *path, int pageNum, char **err) {
    *err = NULL;
    @autoreleasepool {
        NSURL *url = [NSURL fileURLWithPath:[NSString stringWithUTF8String:path]];
        CGPDFDocumentRef doc = CGPDFDocumentCreateWithURL((__bridge CFURLRef)url);
        if (doc == NULL) { *err = strdup("PDF cannot be opened"); return NULL; }
        CGPDFPageRef page = CGPDFDocumentGetPage(doc, (size_t)pageNum);
        if (page == NULL) { CGPDFDocumentRelease(doc); *err = strdup("no such page"); return NULL; }

        CGRect box = CGPDFPageGetBoxRect(page, kCGPDFCropBox);
        int rot = CGPDFPageGetRotationAngle(page) % 360;
        double pw = box.size.width, ph = box.size.height;
        if (rot == 90 || rot == 270) { double t = pw; pw = ph; ph = t; }
        double scale = kScale;
        if (pw * scale > kMaxSide) scale = kMaxSide / pw;
        if (ph * scale > kMaxSide) scale = kMaxSide / ph;
        size_t w = (size_t)(pw * scale), h = (size_t)(ph * scale);
        if (w == 0 || h == 0) { CGPDFDocumentRelease(doc); return strdup("[]"); }

        CGColorSpaceRef gray = CGColorSpaceCreateDeviceGray();
        CGContextRef ctx = CGBitmapContextCreate(NULL, w, h, 8, 0, gray, (CGBitmapInfo)kCGImageAlphaNone);
        CGColorSpaceRelease(gray);
        if (ctx == NULL) { CGPDFDocumentRelease(doc); *err = strdup("cannot allocate page image"); return NULL; }
        CGContextSetGrayFillColor(ctx, 1.0, 1.0);
        CGContextFillRect(ctx, CGRectMake(0, 0, w, h));
        CGContextSetInterpolationQuality(ctx, kCGInterpolationHigh);
        // GetDrawingTransform never scales up, so scale the context first
        // and let the transform handle rotation and the box offset.
        CGContextScaleCTM(ctx, scale, scale);
        CGContextConcatCTM(ctx, CGPDFPageGetDrawingTransform(page, kCGPDFCropBox, CGRectMake(0, 0, pw, ph), 0, true));
        CGContextDrawPDFPage(ctx, page);
        CGImageRef img = CGBitmapContextCreateImage(ctx);
        CGContextRelease(ctx);
        CGPDFDocumentRelease(doc);
        if (img == NULL) { *err = strdup("cannot render page"); return NULL; }

        VNRecognizeTextRequest *req = [[VNRecognizeTextRequest alloc] init];
        req.recognitionLevel = VNRequestTextRecognitionLevelAccurate;
        req.usesLanguageCorrection = YES;
        req.recognitionLanguages = @[ @"de-DE", @"en-US" ];
        VNImageRequestHandler *handler = [[VNImageRequestHandler alloc] initWithCGImage:img options:@{}];
        NSError *verr = nil;
        BOOL ok = [handler performRequests:@[ req ] error:&verr];
        CGImageRelease(img);
        if (!ok) {
            *err = dupString(verr ? verr.localizedDescription : @"text recognition failed");
            return NULL;
        }

        NSMutableArray *lines = [NSMutableArray array];
        for (VNRecognizedTextObservation *obs in req.results) {
            VNRecognizedText *best = [[obs topCandidates:1] firstObject];
            if (best == nil || best.string.length == 0) continue;
            CGRect bb = obs.boundingBox; // normalised, origin bottom left
            [lines addObject:@{
                @"t" : best.string,
                @"x" : @(bb.origin.x),
                @"y" : @(1.0 - bb.origin.y - bb.size.height),
                @"h" : @(bb.size.height),
            }];
        }
        NSData *json = [NSJSONSerialization dataWithJSONObject:lines options:0 error:&verr];
        if (json == nil) { *err = strdup("cannot encode result"); return NULL; }
        NSString *s = [[NSString alloc] initWithData:json encoding:NSUTF8StringEncoding];
        return dupString(s);
    }
}
