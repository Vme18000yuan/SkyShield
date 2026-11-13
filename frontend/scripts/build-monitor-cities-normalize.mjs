// Complete build-monitor-cities.py's reproducible Traditional-to-Simplified pass.
import fs from "node:fs"
import OpenCC from "opencc-js/t2cn"
const convert = OpenCC.Converter({ from: "tw", to: "cn" })
const records = JSON.parse(fs.readFileSync(process.argv[2], "utf8"))
for (const record of records) record[3] = convert(record[3])
fs.writeFileSync(process.argv[3], JSON.stringify(records) + "\n")
console.log(`Wrote ${records.length} cities to ${process.argv[3]}`)
