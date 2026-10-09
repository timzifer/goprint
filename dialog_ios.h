// UIKit printing for goprint on iOS (dialog_ios.m). Every call returns at
// once and reports through goprintIOSDone, on the main thread, when
// UIKit's completion handler runs. The arguments are copied before the
// call returns.

#include <stddef.h>
#include <stdint.h>

// goprint_ios_is_main reports whether the caller is on the main thread.
int goprint_ios_is_main(void);

// goprint_ios_present shows the print sheet for the PDF. duplex is -1 for
// the printer's default; owner is a UIView* to present from on iPad, or 0.
void goprint_ios_present(uintptr_t id, const void *pdf, size_t len,
	const char *jobName, const char *printerID, int duplex, int orientation, int outputType,
	uintptr_t owner);

// goprint_ios_pick shows the printer picker.
void goprint_ios_pick(uintptr_t id, const char *printerID, uintptr_t owner);

// goprint_ios_print_to prints the PDF to the printer at printerID without
// UI.
void goprint_ios_print_to(uintptr_t id, const void *pdf, size_t len,
	const char *jobName, const char *printerID, int duplex, int orientation, int outputType);
