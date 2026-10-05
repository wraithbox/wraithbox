// X05-fs-benchmark spike: the kill-mid-write test, guest side. Throwaway.
//
//   node crash.mjs write <dir>   append 4 KiB records to <dir>/records.bin,
//                                fsync after each (F_FULLFSYNC through
//                                libuv), and print "ack <n>" once record n
//                                is synced. In between, write 64 KiB files
//                                to <dir>/files/ without fsync and print
//                                "file <n>" when write() returns. Runs until
//                                the VM dies.
//   node crash.mjs check <dir>   after the next boot: how many records are
//                                intact and in order, the first bad one,
//                                and how many of the unsynced files exist
//                                with the right content. One JSON line.
import fs from "node:fs";
import crypto from "node:crypto";

const [cmd, dir] = process.argv.slice(2);
const REC = 4096;

function record(n) {
  const b = Buffer.alloc(REC, 0);
  b.writeUInt32LE(0x58303552, 0); // "R50X"
  b.writeUInt32LE(n, 4);
  crypto.createHash("sha256").update(`x05-${n}`).digest().copy(b, 8);
  b.fill(n & 0xff, 40, REC - 32);
  crypto.createHash("sha256").update(b.subarray(0, REC - 32)).digest().copy(b, REC - 32);
  return b;
}

function fileBody(n) {
  return Buffer.alloc(65536, `file-${n}-`);
}

if (cmd === "write") {
  fs.mkdirSync(`${dir}/files`, { recursive: true });
  const fd = fs.openSync(`${dir}/records.bin`, "w");
  for (let n = 0; ; n++) {
    fs.writeSync(fd, record(n));
    fs.fsyncSync(fd);
    process.stdout.write(`ack ${n}\n`);
    fs.writeFileSync(`${dir}/files/${n}`, fileBody(n));
    process.stdout.write(`file ${n}\n`);
  }
} else if (cmd === "check") {
  let intact = 0;
  let firstBad = null;
  let size = 0;
  try {
    const all = fs.readFileSync(`${dir}/records.bin`);
    size = all.length;
    for (let n = 0; (n + 1) * REC <= all.length; n++) {
      if (!all.subarray(n * REC, (n + 1) * REC).equals(record(n))) {
        firstBad = n;
        break;
      }
      intact++;
    }
  } catch (e) {
    firstBad = `records.bin: ${e.code}`;
  }
  let files = 0;
  let badFiles = 0;
  let maxFile = -1;
  try {
    for (const f of fs.readdirSync(`${dir}/files`)) {
      const n = Number(f);
      const ok = fs.readFileSync(`${dir}/files/${f}`).equals(fileBody(n));
      if (ok) files++;
      else badFiles++;
      if (n > maxFile) maxFile = n;
    }
  } catch (e) {
    badFiles = `files: ${e.code}`;
  }
  process.stdout.write(JSON.stringify({ recordsBytes: size, recordsIntact: intact, firstBad, files, badFiles, maxFile }) + "\n");
}
