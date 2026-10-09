#!/bin/sh
# gen.sh builds classes.dex from java/ with pinned tools, so that CI can
# check that the committed file matches the source:
#
#   platforms;android-34 and build-tools;35.0.0 from $ANDROID_HOME,
#   javac of JDK 21 targeting Java 8.
#
# Run it after changing the Java source and commit classes.dex.
set -eu
cd "$(dirname "$0")"
sdk=${ANDROID_HOME:?ANDROID_HOME must point to the Android SDK}
jar=$sdk/platforms/android-34/android.jar
d8=$sdk/build-tools/35.0.0/d8
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
javac --release 8 -Xlint:-options -classpath "$jar" -d "$tmp/classes" $(find java -name '*.java')
"$d8" --release --min-api 26 --lib "$jar" --output "$tmp" $(find "$tmp/classes" -name '*.class')
cp "$tmp/classes.dex" classes.dex
