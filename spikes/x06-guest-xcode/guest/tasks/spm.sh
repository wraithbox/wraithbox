# X06 task: SwiftPM build and test of a package with a resource bundle.
cd ~/work/X06Pkg || exit 1
rm -rf .build-x06
t0=$(date +%s)
swift build --scratch-path .build-x06 > ~/work/spm-build.log 2>&1; b=$?
t1=$(date +%s)
swift test --scratch-path .build-x06 > ~/work/spm-test.log 2>&1; t=$?
t2=$(date +%s)
tail -4 ~/work/spm-build.log
tail -12 ~/work/spm-test.log
ls -d .build-x06/debug/*.bundle
echo "RESULT spm build exit=$b $((t1 - t0))s, test exit=$t $((t2 - t1))s"
[ $b -eq 0 ] && [ $t -eq 0 ]
