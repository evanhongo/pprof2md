# Workflow

## Scenario: CPU Profile

We profiled a Node.js HTTP server for 10 seconds. The profiler collected **500 call stack samples**. The profile stores them as 3 sample records, one for each unique stack.

| Samples | Call stack (leaf to root)                                         |
| ------- | ----------------------------------------------------------------- |
| 300     | `JSON.parse` ← `parseBody` ← `processRequest` ← `handleHTTP`      |
| 150     | `RegExp.exec` ← `validateEmail` ← `processRequest` ← `handleHTTP` |
| 50      | `processRequest` (the leaf is the function itself) ← `handleHTTP` |

### 1. parser

#### Input: the raw `.pb.gz` binary

The pprof protobuf format has one key design feature. **The StringTable holds all strings. Other fields refer to a string only by its index in the StringTable.**

Raw protobuf (simplified):

```
stringTable: [
  /* 0 */ "",
  /* 1 */ "samples",
  /* 2 */ "count",
  /* 3 */ "JSON.parse",
  /* 4 */ "parseBody",
  /* 5 */ "processRequest",
  /* 6 */ "handleHTTP",
  /* 7 */ "handler.ts",
  ...                     // "RegExp.exec", "validateEmail", ...
]

function[1]: { id:1, name:3, filename:0, startLine:0   }  // "JSON.parse" (native: no file)
function[2]: { id:2, name:4, filename:7, startLine:140 }  // "parseBody"
function[3]: { id:3, name:5, filename:7, startLine:85  }  // "processRequest"
function[4]: { id:4, name:6, filename:7, startLine:20  }  // "handleHTTP"
// function[5] = "RegExp.exec", function[6] = "validateEmail" (not shown)

location[1]: { id:1, line:[{functionId:1, line:0}] }
location[2]: { id:2, line:[{functionId:2, line:142}] }
location[3]: { id:3, line:[{functionId:3, line:88}] }
location[4]: { id:4, line:[{functionId:4, line:25}] }
// location[5] = RegExp.exec, location[6] = validateEmail at line 152 (not shown)

sample[1]: { locationId:[1,2,3,4], value:[300] }  // 300 samples
sample[2]: { locationId:[5,6,3,4], value:[150] }
sample[3]: { locationId:[3,4],     value:[50] }
```

All 3 samples use location 3 (`processRequest` at line 88). Line 88 calls both `parseBody` and `validateEmail` (see the source code in step 4).

#### What `Parse()` does

`profile.ParseData` of `github.com/google/pprof` decompresses the data, decodes the protobuf and replaces the indexes with the real strings and records. Then `normalize()` copies the result into a `parser.Profile`. Each line of a stack holds a copy of its function, so the next steps can use the objects directly.

```go
// Output: *parser.Profile
&parser.Profile{
	SampleTypes:   []SampleType{{Type: "samples", Unit: "count"}},
	DurationNanos: 10_000_000_000, // 10 seconds

	Functions: map[uint64]FunctionInfo{
		1: {ID: 1, Name: "JSON.parse",     Filename: "",           StartLine: 0},
		2: {ID: 2, Name: "parseBody",      Filename: "handler.ts", StartLine: 140},
		3: {ID: 3, Name: "processRequest", Filename: "handler.ts", StartLine: 85},
		4: {ID: 4, Name: "handleHTTP",     Filename: "handler.ts", StartLine: 20},
		// ...
	},

	Samples: []Sample{
		{
			Stack: []Location{ // leaf first
				{Lines: []LineInfo{{Function: FunctionInfo{Name: "JSON.parse",     Filename: ""},           Line: 0}}},
				{Lines: []LineInfo{{Function: FunctionInfo{Name: "parseBody",      Filename: "handler.ts"}, Line: 142}}},
				{Lines: []LineInfo{{Function: FunctionInfo{Name: "processRequest", Filename: "handler.ts"}, Line: 88}}},
				{Lines: []LineInfo{{Function: FunctionInfo{Name: "handleHTTP",     Filename: "handler.ts"}, Line: 25}}},
			},
			Values: []int64{300},
		},
		// ... the other two samples
	},
}
```

### 2. analyzer

#### `aggregateFunctionStats()` — calculate the time share of each function

```
Total value = 300 + 150 + 50 = 500
```

The analyzer uses one sample type of the profile. If the profile names a default sample type, the analyzer uses it (Go's `allocs` profile names `alloc_space`). If not, for a CPU profile it uses time and not the sample count, as pprof does: `cpu` (Go), else another time (V8 time profiles have only `wall`), else the sample count (`samples` or `sample`). For other profiles, or when the profile has none of these types, it uses the last sample type, as pprof does: `inuse_space` in a Go heap profile, `alloc_space` in a V8 heap profile with allocations, `space` in a V8 heap profile without allocations, and `delay` in a Go mutex or block profile. This example has only `samples`.

All percents of cost (self and cumulative) are relative to the total value. A diff profile (`go tool pprof -proto -diff_base`) has negative values for the base samples, and pprof labels them `pprof::base`. For a diff profile, the total value is the total of the base profile (as in pprof), and percents have a sign. The rankings then use the size of the percents, so regressions and improvements show together.

The function goes through the stack of each sample:

- **Frames**: The analyzer expands each location into its source lines. When the compiler inlined a function, one location has more than one line, and pprof lists the innermost line first. So the frames stay in leaf-first order.
- **Self time**: The analyzer counts self time only for the first frame (the leaf). Inlined callers in the same location do not get self time.
- **Cumulative time**: The analyzer adds the value of the sample to each function in the stack. If a function occurs more than one time in the stack (recursion), the analyzer counts it only one time for that sample.
- **Key**: The analyzer identifies a function by `name@filename:StartLine`. Go profiles set `StartLine`, and `line` is the sampled line in the function, so all lines of one function make one entry. The 3 frames of `processRequest` have `StartLine` 85, so they make one entry.
  - Some profilers set `StartLine` to 0: V8 exporters (for example `@datadog/pprof`), older Go releases, and some Rust and Java exporters. Then the key is `name@filename:0`, and all lines of one function make one entry. When a label must tell two functions with the same name apart, it shows only the file (for example `hot (a/hot.rs)`).
  - **Limit**: If `StartLine` is 0, two functions with the same name in one file make one entry. Examples are two getters `get` in one file, and the two `handle` functions in `router/index.js` of Express. V8 exporters set `line` to the line of the function, but other profilers set it to the sampled line. The analyzer cannot tell these two cases apart without a guess, so it does not use `line` in the key.
- **Line**: The line that the analyzer shows for a function is the line with the most self time. If the function has no self time, it is the line with the most cumulative time (the hottest call site).

| Function         | self samples | self%   | cumulative samples | cum% |
| ---------------- | ------------ | ------- | ------------------ | ---- |
| `JSON.parse`     | 300          | **60%** | 300                | 60%  |
| `RegExp.exec`    | 150          | **30%** | 150                | 30%  |
| `processRequest` | 50           | **10%** | 500                | 100% |
| `parseBody`      | 0            | 0%      | 300                | 60%  |
| `validateEmail`  | 0            | 0%      | 150                | 30%  |
| `handleHTTP`     | 0            | 0%      | 500                | 100% |

#### `buildCallTree()` — build the call tree

The function combines all samples into one tree. The tree grows from the root down. Each node has the cumulative value of the samples that go through it. The detail format uses the tree to find the **Call path** of a function: the path to the node of the function with the largest cumulative value.

```
(root)  [cum=500]
└── handleHTTP  [cum=500]
    └── processRequest  [cum=500]
        ├── parseBody  [cum=300]
        │   └── JSON.parse  [cum=300]
        └── validateEmail  [cum=150]
            └── RegExp.exec  [cum=150]
```

#### `identifyHotspots()` — select the hotspots

The function selects each function that has a self cost, however small. `parseBody`, `validateEmail` and `handleHTTP` have 0% self, so they are not hotspots. The formatter then shows the top functions of the list (`--max-self-count`).

```go
hotspots = []Hotspot{
	{Name: "JSON.parse",     SelfPercent: 60, CumulativePercent: 60},
	{Name: "RegExp.exec",    SelfPercent: 30, CumulativePercent: 30},
	{Name: "processRequest", SelfPercent: 10, CumulativePercent: 100},
}
```

#### `identifyCumulativeHotspots()` — rank by cumulative time

The function selects each function that has a cumulative cost, and ranks them by cumulative%. If two functions have the same cumulative%, the function with more self% comes first, because it does the work that the other function only passes through. `processRequest` (10% self) thus comes before `handleHTTP` (0% self). For a diff profile, the function compares the sizes of the percents, as for the hotspots.

```
processRequest 100%, handleHTTP 100%, JSON.parse 60%, parseBody 60%, RegExp.exec 30%, validateEmail 30%
```

### 3. formatter

#### Words for the cost

The title and the words for the cost come from the profile type. For a heap profile, the share also comes from the sample type that the analyzer uses (see below):

| Profile type                                 | Title            | Self cost         | Cumulative cost         | Percents are a share of |
| -------------------------------------------- | ---------------- | ----------------- | ----------------------- | ----------------------- |
| CPU profile (`cpu`, `wall` in V8, `samples`) | `CPU`            | `self-time`       | `cumulative-time`       | `CPU`                   |
| heap profile                                 | `HEAP`           | `self-allocation` | `cumulative-allocation` | see below               |
| mutex or block profile (`delay`)             | `MUTEX`          | `self-time`       | `cumulative-time`       | `contention`            |
| other profiles                               | the profile type | `self`            | `cumulative`            | `the total`             |

For a heap profile, the words depend on the sample type, because allocated memory and in-use memory tell different things. Allocated memory includes the memory that was freed again, so it shows allocation churn and work for the garbage collector. In-use memory shows what the program keeps.

| Sample type                 | Percents are a share of | **Measures:** line in the header                                         |
| --------------------------- | ----------------------- | ------------------------------------------------------------------------ |
| `alloc_space`               | `allocated bytes`       | all memory allocated during profiling, including memory freed since      |
| `alloc_objects`             | `allocated objects`     | all objects allocated during profiling, including objects freed since    |
| `inuse_space`, `space`      | `in-use bytes`          | memory allocated and not yet freed when the profile was taken            |
| `inuse_objects`, `objects`  | `in-use objects`        | objects allocated and not yet freed when the profile was taken           |
| other                       | `memory`                | (no line)                                                                |

`space` and `objects` are the sample types of a V8 heap profile without allocations.

#### Call chains

The **Call path** of each function in the detail format is a call chain. A chain collapses each long run of library frames. Library frames are all frames that are not application code: runtime, library and native code (see the kinds of code in [Output of the `detail` format](#output-of-the-detail-format-default)).

- The first and the last frame of a run stay. They show where the chain starts and ends, and where it goes into and out of application code.
- The frames between them change to `… N library frames …`, but only if N is 3 or more. For fewer frames, the text saves almost nothing.
- Labels tell apart only the frames that stay. For example, if only one of two `handle` functions stays, its label is `handle`, not `handle (router/index.js)`.
- The `(root)` node of a call path stays, and it ends a run.

For example, Node and Express call a route handler through 16 frames:

```
`(root)` → `parserOnHeadersComplete` → … 14 library frames … → `handleRequest` → `(anonymous:L#6:C#25)`
```

The chains in the example of this document have no long runs, so they do not change.

#### Output of the `summary` format

The summary format has only the **Executive Summary**. The Executive Summary has two rankings: the functions with the most self time, and the functions with the most cumulative time. Each ranking shows 5 functions (`--max-self-count` and `--max-cumulative-count` change these numbers). The first item of a ranking tells what the percents are a share of.

```markdown
# PPROF Analysis: CPU

**Profile:** `server.pb.gz`
**Duration:** 10.0s | **Samples:** 500 | **Type:** samples (count)

## Executive Summary

### Top self-time

1. `JSON.parse` (**60.0%** of CPU)
2. `RegExp.exec` (**30.0%**)
3. `processRequest` (**10.0%**)

### Top cumulative-time

1. `processRequest` (**100.0%** of CPU)
2. `handleHTTP` (**100.0%**)
3. `JSON.parse` (**60.0%**)
4. `parseBody` (**60.0%**)
5. `RegExp.exec` (**30.0%**)
```

Only 3 functions have self time, so the self-time ranking has 3 items. `validateEmail` is sixth by cumulative time, so it does not show.

> **Samples: 500** is the number of call stack samples. One sample record stands for all samples of one stack. If the profile has a sample count (`samples/count` in Go CPU profiles, `sample/count` in V8 time profiles), the analyzer adds the counts of the records. If not, each record is one sample. Records with the value 0 for the sample type (for example, memory that was freed, in `inuse_space`) are not counted.
>
> **Duration** shows only if the profile records one. Heap profiles usually do not.
>
> **Total sample time** shows only if the unit of the sample type is a time (`nanoseconds`, `ns`, `microseconds`, `milliseconds` or `seconds`), for example `cpu (nanoseconds)` in a Go CPU profile, `wall (nanoseconds)` in a V8 time profile or `delay (nanoseconds)` in a Go mutex or block profile. It is the total value, that is, the time of all samples together. It shows between **Samples** and **Type**:
>
> ```markdown
> **Duration:** 30.0s | **Samples:** 2,950 | **Total sample time:** 29.5s | **Type:** cpu (nanoseconds)
> ```
>
> The time has the format of **Duration** (`10.0s`, `1m 5s`). A time of less than 1 second shows in `ms`, `µs` or `ns`, for example `12.5ms`. The example of this document uses `samples (count)`, so its header has no **Total sample time**. A diff has no **Total sample time**, because its total value is the total of the base profile.
>
> A heap profile has no samples to count: each record is a stack, not a sample. Thus its header has a **Measures:** line (see [Words for the cost](#words-for-the-cost)) and a **Total** in place of **Samples** and **Type**. The total is the bytes, with the objects of the same kind if the profile counts them (`alloc_objects` with `alloc_space`, `inuse_objects` with `inuse_space`, `objects` with `space`). If the analyzer uses objects, the total is only the objects. For example:
>
> ```markdown
> # PPROF Analysis: HEAP
>
> **Profile:** `profile.pb.gz`
> **Measures:** allocated bytes (`alloc_space`): all memory allocated during profiling, including memory freed since
> **Total:** 2.6 GB in 37,362,578 objects
> ```
>
> Bytes use binary units, as in pprof: 1 KB is 1,024 bytes. A diff has no **Total**, because its percents are relative to the base total.

#### Output of the `detail` format (default)

The `detail` format is the default. It has:

- The **Executive Summary** of the summary format. Each function in it has a **drill-down link with an anchor** to its section.
- A **Detailed Analysis** section for each function in the Executive Summary: its **Location** (not for native code), call path, self cost, cumulative cost, kind of code and source code. A function that is in both rankings has one section. The sections of the self-time ranking come first, then the other sections of the cumulative-time ranking.

```markdown
# PPROF Analysis: CPU

**Profile:** `server.pb.gz`
**Duration:** 10.0s | **Samples:** 500 | **Type:** samples (count)

## Executive Summary

### Top self-time

1. `JSON.parse` (**60.0%** of CPU) → [Details](#json-parse)
2. `RegExp.exec` (**30.0%**) → [Details](#regexp-exec)
3. `processRequest` (**10.0%**) → [Details](#processrequest)

### Top cumulative-time

1. `processRequest` (**100.0%** of CPU) → [Details](#processrequest)
2. `handleHTTP` (**100.0%**) → [Details](#handlehttp)
3. `JSON.parse` (**60.0%**) → [Details](#json-parse)
4. `parseBody` (**60.0%**) → [Details](#parsebody)
5. `RegExp.exec` (**30.0%**) → [Details](#regexp-exec)

---

## Detailed Analysis

<a id="json-parse"></a>

### `JSON.parse`

**Call path:** `(root)` → `handleHTTP` → `processRequest` → `parseBody` → `JSON.parse`
**Self-time:** 60.0% (300 samples)
**Cumulative-time:** 60.0% (300 samples)
**Type:** Native function

<!-- The sections for RegExp.exec, processRequest, handleHTTP and parseBody are not shown here. -->
```

> **Type** is the kind of code. The analyzer puts each function in one of 3 kinds:
>
> - **Native**: no source file that you can read (VM built-ins such as `JSON.parse`, native code, garbage collection, `wasm://` code, and the Go wrappers that cgo generates for C functions, such as `_Cfunc_burn` in `_cgo_gotypes.go`).
> - **Runtime or library code**: Node built-ins (`node:*`), `node_modules`, `vendor/`, Go modules (a Go file with a module version such as `@v1.2.3` or a pseudo-version in its path, in the module cache or in a `-trimpath` build) and the Go standard library (found from the first element of the import path, so it also works with `-trimpath`). The main module is application code, also when it has a version: a binary from `go install example.com/tool@v1.4.0` runs its own code from the module cache. The analyzer finds the main module from the functions of package `main` in the profile. If the profile has no function of package `main` (for example, a server whose main goroutine only waits) and no Go file outside the module cache, the standard library and `vendor/` (files that cgo generates do not count), the analyzer uses the name of the executable in the first mapping of the profile (`tool` for `example.com/tool@v1.4.0` and `example.com/tool/v2@v2.1.0`). It does not use the name in other cases, because a binary built from local source code can have the same name as a dependency (for example, a `cli` tool that uses `github.com/urfave/cli`). If neither works, for example after a rename of the binary, the main module is library code.
> - **Application code**: all other functions.
>
> Code that the analyzer cannot identify from its path is application code. Examples are code from `vm.runInContext` (`evalmachine.<anonymous>`) and bundled files such as `dist/bundle.js`, which can contain dependencies. Other limits:
>
> - The list of standard-library packages comes from Go 1.27. A test compares it with `go list std` of the installed Go, so add new top-level packages when a new Go release adds them. Until then, the analyzer thinks that they are application code.
> - An application module whose path has no dot and starts like a standard package (for example `module net` or `module text`) is library code. Go reserves paths without a dot for the standard library.
>
> **Location** is the file of the function and its line. For library code, the important information is the package or the Node module:
>
> - A Node built-in shows its full module specifier, for example `node:internal/crypto/hash:184`.
> - A file in `node_modules` shows its path from the package name, for example `express/lib/response.js:1026` or `@scope/pkg/lib/parse.js:10`. If `node_modules` directories are nested, the path starts after the last one (`a/node_modules/b/index.js` shows as `b/index.js`).
> - Other files show their base name, with as many parent directories as are necessary to tell them apart from the other files of the profile (for example `users/index.ts:12` and `orders/index.ts:30`).
> - Native code has no file. Its section has no **Location**.
>
> The section of `processRequest` has `**Location:** \`handler.ts:88\``.
>
> In **Self-time**, the number in parentheses is the number of samples where the function is the leaf (its self samples), counted as for **Samples**. `JSON.parse` is the leaf in 300 samples. In **Cumulative-time**, the number in parentheses is the number of samples whose stack contains the function. All 500 stacks contain `processRequest`, so its section has `**Cumulative-time:** 100.0% (500 samples)`.
>
> In a heap profile, the parentheses show the amount in place of samples: the bytes and the objects, for example `**Self-allocation:** 18.8% (504.4 MB, 12,135,722 objects)`. If the analyzer uses objects (`alloc_objects`), they show only the objects. If the profile has no object count, they show only the bytes.
>
> For a diff profile, the rankings are about changes: "Largest self-time changes" in place of "Top self-time", and "Largest cumulative-time changes" in place of "Top cumulative-time". The percent is relative to the base total, like all percents of a diff.

### 4. source-resolver

The detail format always shows the source code that the source resolver finds. The resolver looks for the source code of each non-native function in the Executive Summary. It looks under the directory of `--source-dir`, or under the current directory if there is no `--source-dir`. In this profile, there are 3 such functions: `processRequest @ handler.ts:88`, `handleHTTP @ handler.ts:25` and `parseBody @ handler.ts:142`. This example uses `--source-dir=./src` and shows `processRequest`.

#### How the resolver finds the file

```
hotspot.Filename = "handler.ts"
hotspot.Line     = 88
sourceDir        = "./src"

Order of attempts:
  1. filepath.IsAbs?  → No
  2. filepath.Join(sourceDir, "handler.ts")  → ./src/handler.ts  ✅ found
```

#### Extract 3 lines before and after (contextLines = 3)

```
// Content of handler.ts (lines 85 to 91)
85: export function processRequest(raw: string) {
86:   const started = Date.now()
87:
88:   const user = validateEmail(parseBody(raw))      ← hotspot.Line = 88
89:   return { status: 'ok', id: user.id, ms: Date.now() - started }
90: }
91:

start = max(1, 88-3)              = 85
end   = min(len(allLines), 88+3)  = 91
```

#### How the source looks in the detail Markdown

This part is in the Detailed Analysis section of `processRequest`:

````markdown
**Source:**

```
// handler.ts:85-91
export function processRequest(raw: string) {
  const started = Date.now()

  const user = validateEmail(parseBody(raw)) // ← HOT
  return { status: 'ok', id: user.id, ms: Date.now() - started }
}

```
````
