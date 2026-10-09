//go:build ios

#import <UIKit/UIKit.h>

#include "dialog_ios.h"
#include "_cgo_export.h"

int goprint_ios_is_main(void) {
	return [NSThread isMainThread] ? 1 : 0;
}

// report hands a result to Go. The strings are only read during the call.
static void report(uintptr_t id, int status, NSString *msg, UIPrintInfo *info) {
	goprintIOSDone(id, status,
		(char *)(msg ? msg.UTF8String : ""),
		(char *)(info.printerID ? info.printerID.UTF8String : ""),
		info ? (int)info.duplex : -1,
		info ? (int)info.orientation : 0,
		info ? (int)info.outputType : 0);
}

static NSString *str(const char *s) {
	return s ? [NSString stringWithUTF8String:s] : @"";
}

static UIPrintInfo *makeInfo(NSString *jobName, NSString *printerID, int duplex, int orientation, int outputType) {
	UIPrintInfo *info = [UIPrintInfo printInfo];
	info.jobName = jobName;
	info.outputType = (UIPrintInfoOutputType)outputType;
	info.orientation = (UIPrintInfoOrientation)orientation;
	if (duplex >= 0) {
		info.duplex = (UIPrintInfoDuplex)duplex;
	}
	if (printerID.length > 0) {
		info.printerID = printerID;
	}
	return info;
}

// anchorView is the view to present from on iPad: the owner, or the root
// view of the key window.
static UIView *anchorView(uintptr_t owner) {
	if (owner != 0) {
		return (__bridge UIView *)(void *)owner;
	}
	for (UIScene *scene in UIApplication.sharedApplication.connectedScenes) {
		if (![scene isKindOfClass:[UIWindowScene class]]) {
			continue;
		}
		for (UIWindow *w in ((UIWindowScene *)scene).windows) {
			if (w.isKeyWindow) {
				return w.rootViewController.view ? w.rootViewController.view : w;
			}
		}
	}
	return nil;
}

static BOOL onPad(void) {
	return UIDevice.currentDevice.userInterfaceIdiom == UIUserInterfaceIdiomPad;
}

static CGRect centerOf(UIView *v) {
	return CGRectMake(CGRectGetMidX(v.bounds), CGRectGetMidY(v.bounds), 1, 1);
}

void goprint_ios_present(uintptr_t id, const void *pdf, size_t len,
	const char *jobName, const char *printerID, int duplex, int orientation, int outputType,
	uintptr_t owner) {
	NSData *data = [NSData dataWithBytes:pdf length:len];
	NSString *name = str(jobName), *pid = str(printerID);
	dispatch_async(dispatch_get_main_queue(), ^{
		if (![UIPrintInteractionController canPrintData:data]) {
			report(id, 3, @"iOS cannot print this document", nil);
			return;
		}
		UIPrintInteractionController *pc = [UIPrintInteractionController sharedPrintController];
		pc.printInfo = makeInfo(name, pid, duplex, orientation, outputType);
		pc.printingItem = data;
		pc.showsNumberOfCopies = YES;
		pc.showsPaperSelectionForLoadedPapers = YES;
		UIPrintInteractionCompletionHandler done = ^(UIPrintInteractionController *c, BOOL completed, NSError *err) {
			if (err) {
				report(id, 2, err.localizedDescription, c.printInfo);
			} else {
				report(id, completed ? 0 : 1, nil, c.printInfo);
			}
		};
		UIView *view = anchorView(owner);
		BOOL shown = (onPad() && view)
			? [pc presentFromRect:centerOf(view) inView:view animated:YES completionHandler:done]
			: [pc presentAnimated:YES completionHandler:done];
		if (!shown) {
			report(id, 3, @"the print sheet could not be shown", nil);
		}
	});
}

void goprint_ios_pick(uintptr_t id, const char *printerID, uintptr_t owner) {
	NSString *pid = str(printerID);
	dispatch_async(dispatch_get_main_queue(), ^{
		UIPrinter *initial = nil;
		if (pid.length > 0) {
			NSURL *url = [NSURL URLWithString:pid];
			if (url) {
				initial = [UIPrinter printerWithURL:url];
			}
		}
		UIPrinterPickerController *pp = [UIPrinterPickerController printerPickerControllerWithInitiallySelectedPrinter:initial];
		UIPrinterPickerCompletionHandler done = ^(UIPrinterPickerController *c, BOOL selected, NSError *err) {
			if (err) {
				report(id, 2, err.localizedDescription, nil);
			} else if (!selected || !c.selectedPrinter) {
				report(id, 1, nil, nil);
			} else {
				UIPrintInfo *info = [UIPrintInfo printInfo];
				info.printerID = c.selectedPrinter.URL.absoluteString;
				report(id, 0, nil, info);
			}
		};
		UIView *view = anchorView(owner);
		BOOL shown = (onPad() && view)
			? [pp presentFromRect:centerOf(view) inView:view animated:YES completionHandler:done]
			: [pp presentAnimated:YES completionHandler:done];
		if (!shown) {
			report(id, 3, @"the printer picker could not be shown", nil);
		}
	});
}

void goprint_ios_print_to(uintptr_t id, const void *pdf, size_t len,
	const char *jobName, const char *printerID, int duplex, int orientation, int outputType) {
	NSData *data = [NSData dataWithBytes:pdf length:len];
	NSString *name = str(jobName), *pid = str(printerID);
	dispatch_async(dispatch_get_main_queue(), ^{
		NSURL *url = [NSURL URLWithString:pid];
		UIPrinter *printer = url ? [UIPrinter printerWithURL:url] : nil;
		if (!printer) {
			report(id, 2, [NSString stringWithFormat:@"invalid printer URL %@", pid], nil);
			return;
		}
		UIPrintInteractionController *pc = [UIPrintInteractionController sharedPrintController];
		pc.printInfo = makeInfo(name, pid, duplex, orientation, outputType);
		pc.printingItem = data;
		BOOL started = [pc printToPrinter:printer completionHandler:^(UIPrintInteractionController *c, BOOL completed, NSError *err) {
			if (err) {
				report(id, 2, err.localizedDescription, c.printInfo);
			} else {
				report(id, completed ? 0 : 1, nil, c.printInfo);
			}
		}];
		if (!started) {
			report(id, 2, [NSString stringWithFormat:@"iOS could not print to %@", pid], nil);
		}
	});
}
