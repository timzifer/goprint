//go:build android && cgo

package goprint

/*
#include <jni.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

// goprintAndroidDone is exported by dialog_android.go.
extern void goprintAndroidDone(uintptr_t id, uintptr_t adapter, int *info, int n, char *err);

static jclass adapterClass;
static jmethodID startID, stateID, cancelID;

// takeException clears a pending Java exception and returns its text
// (malloc'ed), or NULL if there is none.
static char *takeException(JNIEnv *env) {
	if (!(*env)->ExceptionCheck(env)) {
		return NULL;
	}
	jthrowable e = (*env)->ExceptionOccurred(env);
	(*env)->ExceptionClear(env);
	jclass c = (*env)->GetObjectClass(env, e);
	jmethodID ts = (*env)->GetMethodID(env, c, "toString", "()Ljava/lang/String;");
	jstring s = ts ? (jstring)(*env)->CallObjectMethod(env, e, ts) : NULL;
	if ((*env)->ExceptionCheck(env) || s == NULL) {
		(*env)->ExceptionClear(env);
		return strdup("Java exception");
	}
	const char *u = (*env)->GetStringUTFChars(env, s, NULL);
	char *r = strdup(u ? u : "Java exception");
	if (u) {
		(*env)->ReleaseStringUTFChars(env, s, u);
	}
	return r;
}

static void JNICALL nativeDone(JNIEnv *env, jclass cls, jlong id, jobject adapter, jintArray info, jstring err) {
	jsize n = info ? (*env)->GetArrayLength(env, info) : 0;
	jint *p = info ? (*env)->GetIntArrayElements(env, info, NULL) : NULL;
	const char *msg = err ? (*env)->GetStringUTFChars(env, err, NULL) : NULL;
	jobject ref = adapter ? (*env)->NewGlobalRef(env, adapter) : NULL;
	goprintAndroidDone((uintptr_t)id, (uintptr_t)ref, (int *)p, (int)n, (char *)msg);
	if (msg) {
		(*env)->ReleaseStringUTFChars(env, err, msg);
	}
	if (p) {
		(*env)->ReleaseIntArrayElements(env, info, p, JNI_ABORT);
	}
}

// goprint_android_load loads PDFAdapter from the dex with an
// InMemoryDexClassLoader below the app's class loader, once. The dex
// must stay valid for the life of the process.
static char *goprint_android_load(uintptr_t envp, uintptr_t ctx, void *dex, int len) {
	JNIEnv *env = (JNIEnv *)envp;
	if (adapterClass) {
		return NULL;
	}
	char *e;
	jobject buf = (*env)->NewDirectByteBuffer(env, dex, len);
	jclass ctxClass = (*env)->GetObjectClass(env, (jobject)ctx);
	jmethodID gcl = (*env)->GetMethodID(env, ctxClass, "getClassLoader", "()Ljava/lang/ClassLoader;");
	if ((e = takeException(env))) return e;
	jobject parent = (*env)->CallObjectMethod(env, (jobject)ctx, gcl);
	if ((e = takeException(env))) return e;
	jclass imcl = (*env)->FindClass(env, "dalvik/system/InMemoryDexClassLoader");
	if ((e = takeException(env))) return e;
	jmethodID ctor = (*env)->GetMethodID(env, imcl, "<init>", "(Ljava/nio/ByteBuffer;Ljava/lang/ClassLoader;)V");
	if ((e = takeException(env))) return e;
	jobject loader = (*env)->NewObject(env, imcl, ctor, buf, parent);
	if ((e = takeException(env))) return e;
	jclass clc = (*env)->FindClass(env, "java/lang/ClassLoader");
	jmethodID lc = (*env)->GetMethodID(env, clc, "loadClass", "(Ljava/lang/String;)Ljava/lang/Class;");
	if ((e = takeException(env))) return e;
	jstring name = (*env)->NewStringUTF(env, "io.github.timzifer.goprint.PDFAdapter");
	jclass cls = (jclass)(*env)->CallObjectMethod(env, loader, lc, name);
	if ((e = takeException(env))) return e;
	JNINativeMethod m = {"done", "(JLio/github/timzifer/goprint/PDFAdapter;[ILjava/lang/String;)V", (void *)nativeDone};
	(*env)->RegisterNatives(env, cls, &m, 1);
	if ((e = takeException(env))) return e;
	startID = (*env)->GetStaticMethodID(env, cls, "start", "(Landroid/content/Context;[BLjava/lang/String;IIIJ)V");
	stateID = (*env)->GetMethodID(env, cls, "state", "()I");
	cancelID = (*env)->GetMethodID(env, cls, "cancel", "()V");
	if ((e = takeException(env))) return e;
	adapterClass = (jclass)(*env)->NewGlobalRef(env, cls);
	return NULL;
}

// goprint_android_start shows the print dialog; the result comes through
// goprintAndroidDone. pdf and name are copied before it returns.
static char *goprint_android_start(uintptr_t envp, uintptr_t ctx, void *pdf, int len, char *name,
	int color, int duplex, int orientation, uintptr_t id) {
	JNIEnv *env = (JNIEnv *)envp;
	jbyteArray arr = (*env)->NewByteArray(env, len);
	if (arr == NULL) {
		char *e = takeException(env);
		return e ? e : strdup("out of memory");
	}
	(*env)->SetByteArrayRegion(env, arr, 0, len, (const jbyte *)pdf);
	jstring jn = (*env)->NewStringUTF(env, name);
	(*env)->CallStaticVoidMethod(env, adapterClass, startID, (jobject)ctx, arr, jn,
		(jint)color, (jint)duplex, (jint)orientation, (jlong)id);
	return takeException(env);
}

static int goprint_android_state(uintptr_t envp, uintptr_t adapter, char **err) {
	JNIEnv *env = (JNIEnv *)envp;
	jint st = (*env)->CallIntMethod(env, (jobject)adapter, stateID);
	*err = takeException(env);
	return (int)st;
}

static char *goprint_android_cancel(uintptr_t envp, uintptr_t adapter) {
	JNIEnv *env = (JNIEnv *)envp;
	(*env)->CallVoidMethod(env, (jobject)adapter, cancelID);
	return takeException(env);
}

static void goprint_android_release(uintptr_t envp, uintptr_t adapter) {
	JNIEnv *env = (JNIEnv *)envp;
	(*env)->DeleteGlobalRef(env, (jobject)adapter);
}
*/
import "C"

import (
	"errors"
	"sync"
	"unsafe"

	"github.com/timzifer/goprint/internal/androidprint"
)

// cError turns a malloc'ed C error text into an error and frees it.
func cError(e *C.char) error {
	if e == nil {
		return nil
	}
	defer C.free(unsafe.Pointer(e))
	return errors.New("goprint: Android: " + C.GoString(e))
}

// dexCopy is the dex in C memory: the class loader may read it lazily.
var dexCopy = sync.OnceValue(func() unsafe.Pointer { return C.CBytes(androidprint.Dex) })

func jniLoad(env, activity uintptr) error {
	return cError(C.goprint_android_load(C.uintptr_t(env), C.uintptr_t(activity), dexCopy(), C.int(len(androidprint.Dex))))
}

func jniStart(env, activity uintptr, pdf []byte, name string, a androidAttrs, id uintptr) error {
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	return cError(C.goprint_android_start(C.uintptr_t(env), C.uintptr_t(activity),
		unsafe.Pointer(&pdf[0]), C.int(len(pdf)), cname,
		C.int(a.color), C.int(a.duplex), C.int(a.orientation), C.uintptr_t(id)))
}

func jniState(env, adapter uintptr) (int, error) {
	var e *C.char
	st := C.goprint_android_state(C.uintptr_t(env), C.uintptr_t(adapter), &e)
	return int(st), cError(e)
}

func jniCancel(env, adapter uintptr) error {
	return cError(C.goprint_android_cancel(C.uintptr_t(env), C.uintptr_t(adapter)))
}

func jniRelease(env, adapter uintptr) {
	C.goprint_android_release(C.uintptr_t(env), C.uintptr_t(adapter))
}
