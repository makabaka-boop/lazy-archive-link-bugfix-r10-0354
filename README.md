# jsonlink — JSON object-file linker

`jsonlink` is a small command-line linker. Its input is a JSON description of one
or more object files; it does not parse ELF or any other native object format.

## Input format

```json
{
  "objects": [
    {
      "name": "example.o",
      "align": 16,
      "text": "c390",
      "data": "78563412",
      "symbols": [
        {"name": "main", "binding": "strong", "section": "text", "value": 0},
        {"name": "helper", "binding": "weak", "section": "text", "value": 1},
        {"name": "state", "binding": "local", "section": "data", "value": 2}
      ],
      "relocations": [
        {"section": "data", "offset": 0, "type": "ABS32", "symbol": "main"},
        {"section": "text", "offset": 0, "type": "PCREL16", "symbol": "helper"}
      ]
    }
  ]
}
```

- `text` and `data` are byte strings encoded as hexadecimal.
- Object `align` must be 1, 2, 4, 8, or 16 bytes and defaults to 1.
- Symbol `binding` is `local`, `strong`, or `weak`.
- Symbol `value` is an offset inside that object's named section.
- Relocation `type` is:
  - `ABS32`: writes `S + A` as a 32-bit little-endian value.
  - `PCREL16`: writes `S + A - (P + 2)` as a signed 16-bit little-endian value.

Here `S` is the symbol address, `A` is the optional addend, `P` is the patch's
start address, and `P + 2` is the end of the PCREL16 patch.

## Layout and symbol rules

- All non-empty `.text` sections are placed first, followed by all non-empty
  `.data` sections.
- Sections within each group retain input object order.
- Each object chunk is placed at the next offset satisfying that object's
  alignment. Alignment gaps contain zero bytes.
- The image base address is `0x1000`.
- Local symbols are resolved only inside their own object.
- A strong global definition wins over weak definitions.
- Multiple weak definitions resolve to the first input object's definition.
- Multiple strong definitions, unresolved symbols, patches crossing a section,
  ABS32 values outside 32 bits, and PCREL16 values outside signed 16 bits are
  errors.

All validation and patching is completed in memory first. An image file is
published only after a successful link, using a temporary file and atomic
rename. The linker exits with an error and no completed report instead of
leaving a half-linked image.

## Usage

```sh
# Write image to a.out and print the JSON evidence report to stdout.
go run . objects.json

# Explicit output paths.
go run . -o image.bin -report report.json objects.json

# Include the complete linked image as hex in the report.
go run . -o image.bin -image-hex objects.json

# Read JSON from stdin and write the raw image to stdout.
cat objects.json | go run . -o - -report /dev/null -
```

The report contains the chunk layout, global and object-local symbol
addresses, every symbol definition (including unused weak definitions), and
per-relocation before/after bytes with the formula used to produce them.



## 按需静态库

JSON 可用 items 代替 objects：每项恰含 object 或 archive；archive 含唯一 name 及有序 members（原对象格式）。按输入位置扫描，普通对象总是进入链接，库只在其命令位置搜索当前未解析的非 local 重定位引用，不会回溯补上后来对象才产生的引用。

抽取某个成员时先加入它的全部非 local 定义，再加入它的引用；成员内依赖使同一库从开头反复展开，直到一轮不再选中成员。循环依赖由“同一成员至多抽取一次”正常终止。同一轮中，新选中成员产生的引用下一轮才从库首搜索，因此库内首个可提供者胜出；未使用成员即使含重名强符号也不进入后续强/弱符号与重定位检查。

已有 weak 定义视为引用已满足，不会仅为查找 strong 定义而抽取后续库；但成员若因另一个当前未解析引用被选中，其 strong 定义仍按普通对象规则覆盖先前 weak。local 符号只在所属对象内解析，也不会触发库抽取。

最终报告 extracted 给出库、成员及促使首次抽取的最小符号名；布局、地址、重定位和最终字节只包含直接对象和实际抽取成员，且对象按命令位置/实际抽取顺序排列。至多 8 库、32 个库成员、64 个最终对象；objects 与 items 不能混用。原 objects-only 输入不应用这些新接口限制，原有错误报告保持不变。
