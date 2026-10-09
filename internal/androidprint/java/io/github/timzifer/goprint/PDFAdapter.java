// PDFAdapter hands a PDF to Android's print framework for goprint. It is
// compiled to classes.dex (see gen.sh) and loaded at run time with an
// InMemoryDexClassLoader, so apps need no Java build step.
package io.github.timzifer.goprint;

import android.content.Context;
import android.os.Bundle;
import android.os.CancellationSignal;
import android.os.Handler;
import android.os.Looper;
import android.os.ParcelFileDescriptor;
import android.print.PageRange;
import android.print.PrintAttributes;
import android.print.PrintDocumentAdapter;
import android.print.PrintDocumentInfo;
import android.print.PrintJob;
import android.print.PrintJobInfo;
import android.print.PrintManager;
import java.io.FileOutputStream;
import java.io.IOException;
import java.util.concurrent.CountDownLatch;

public final class PDFAdapter extends PrintDocumentAdapter {
    // Job states reported to Go.
    static final int CANCELED = 0, FAILED = 1, PENDING = 2, PROCESSING = 3, COMPLETED = 4;

    private final byte[] pdf;
    private final String name;
    private final long id;
    private PrintJob job; // main thread only
    private boolean reported;

    private PDFAdapter(byte[] pdf, String name, long id) {
        this.pdf = pdf;
        this.name = name;
        this.id = id;
    }

    // done reports the end of the print dialog: the job state and, in
    // info, what the user chose (see result). error is null on success.
    private static native void done(long id, PDFAdapter adapter, int[] info, String error);

    // start shows the print dialog. color, duplex: PrintAttributes
    // constants or 0; orientation: 0 default, 1 landscape, 2 portrait.
    public static void start(final Context ctx, byte[] pdf, String name,
            final int color, final int duplex, final int orientation, long id) {
        final PDFAdapter a = new PDFAdapter(pdf, name, id);
        new Handler(Looper.getMainLooper()).post(new Runnable() {
            @Override
            public void run() {
                try {
                    PrintManager pm = (PrintManager) ctx.getSystemService(Context.PRINT_SERVICE);
                    PrintAttributes.Builder b = new PrintAttributes.Builder();
                    if (color != 0) {
                        b.setColorMode(color);
                    }
                    if (duplex != 0) {
                        b.setDuplexMode(duplex);
                    }
                    if (orientation == 1) {
                        b.setMediaSize(PrintAttributes.MediaSize.UNKNOWN_LANDSCAPE);
                    } else if (orientation == 2) {
                        b.setMediaSize(PrintAttributes.MediaSize.UNKNOWN_PORTRAIT);
                    }
                    a.job = pm.print(a.name, a, b.build());
                } catch (RuntimeException e) {
                    a.report(String.valueOf(e));
                }
            }
        });
    }

    @Override
    public void onLayout(PrintAttributes oldAttrs, PrintAttributes newAttrs, CancellationSignal cancel,
            LayoutResultCallback callback, Bundle extras) {
        if (cancel.isCanceled()) {
            callback.onLayoutCancelled();
            return;
        }
        PrintDocumentInfo info = new PrintDocumentInfo.Builder(name)
                .setContentType(PrintDocumentInfo.CONTENT_TYPE_DOCUMENT)
                .setPageCount(PrintDocumentInfo.PAGE_COUNT_UNKNOWN)
                .build();
        callback.onLayoutFinished(info, !newAttrs.equals(oldAttrs));
    }

    @Override
    public void onWrite(PageRange[] pages, ParcelFileDescriptor dest, CancellationSignal cancel,
            WriteResultCallback callback) {
        // The whole document: the framework keeps the selected pages.
        try (FileOutputStream out = new FileOutputStream(dest.getFileDescriptor())) {
            out.write(pdf);
        } catch (IOException e) {
            callback.onWriteFailed(e.getMessage());
            return;
        }
        if (cancel.isCanceled()) {
            callback.onWriteCancelled();
            return;
        }
        callback.onWriteFinished(new PageRange[] {PageRange.ALL_PAGES});
    }

    @Override
    public void onFinish() {
        report(null);
    }

    private void report(String error) {
        if (reported) {
            return;
        }
        reported = true;
        done(id, this, result(), error);
    }

    // result is {state, copies, colorMode, duplexMode, widthMils,
    // heightMils, landscape, n, start1, end1, ..., startN, endN}; page
    // ranges are 0-based, empty for all pages. Main thread only.
    private int[] result() {
        int state = stateNow();
        PrintJobInfo info = job == null ? null : job.getInfo();
        if (info == null) {
            return new int[] {state, 0, 0, 0, 0, 0, 0, 0};
        }
        PrintAttributes a = info.getAttributes();
        PrintAttributes.MediaSize m = a.getMediaSize();
        PageRange[] pages = info.getPages();
        int n = 0;
        if (pages != null && !(pages.length == 1 && pages[0].equals(PageRange.ALL_PAGES))) {
            n = pages.length;
        }
        int[] r = new int[8 + 2 * n];
        r[0] = state;
        r[1] = info.getCopies();
        r[2] = a.getColorMode();
        r[3] = a.getDuplexMode();
        if (m != null) {
            r[4] = m.getWidthMils();
            r[5] = m.getHeightMils();
            r[6] = m.isPortrait() ? 0 : 1;
        }
        r[7] = n;
        for (int i = 0; i < n; i++) {
            r[8 + 2 * i] = pages[i].getStart();
            r[9 + 2 * i] = pages[i].getEnd();
        }
        return r;
    }

    private int stateNow() {
        if (job == null || job.isCancelled()) {
            return CANCELED;
        }
        if (job.isFailed()) {
            return FAILED;
        }
        if (job.isCompleted()) {
            return COMPLETED;
        }
        if (job.isStarted() || job.isBlocked()) {
            return PROCESSING;
        }
        return PENDING;
    }

    // state returns the job state; it may be called on any thread.
    public int state() {
        final int[] r = new int[1];
        onMain(new Runnable() {
            @Override
            public void run() {
                r[0] = stateNow();
            }
        });
        return r[0];
    }

    // cancel cancels the job; it may be called on any thread.
    public void cancel() {
        onMain(new Runnable() {
            @Override
            public void run() {
                if (job != null) {
                    job.cancel();
                }
            }
        });
    }

    // onMain runs r on the main thread and waits for it.
    private static void onMain(final Runnable r) {
        if (Looper.myLooper() == Looper.getMainLooper()) {
            r.run();
            return;
        }
        final CountDownLatch latch = new CountDownLatch(1);
        new Handler(Looper.getMainLooper()).post(new Runnable() {
            @Override
            public void run() {
                try {
                    r.run();
                } finally {
                    latch.countDown();
                }
            }
        });
        try {
            latch.await();
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
        }
    }
}
