# Sample AGENTS.md file

## Dev environment tips
- Use `pnpm dlx turbo run where <project_name>` to jump to a package instead of scanning with `ls`.
- Run `pnpm install --filter <project_name>` to add the package to your workspace so Vite, ESLint, and TypeScript can see it.
- Use `pnpm create vite@latest <project_name> -- --template react-ts` to spin up a new React + Vite package with TypeScript checks ready.
- Check the name field inside each package's package.json to confirm the right name—skip the top-level one.

## Testing instructions
- Find the CI plan in the .github/workflows folder.
- Run `pnpm turbo run test --filter <project_name>` to run every check defined for that package.
- From the package root you can just call `pnpm test`. The commit should pass all tests before you merge.
- To focus on one step, add the Vitest pattern: `pnpm vitest run -t "<test name>"`.
- Fix any test or type errors until the whole suite is green.
- After moving files or changing imports, run `pnpm lint --filter <project_name>` to be sure ESLint and TypeScript rules still pass.
- Add or update tests for the code you change, even if nobody asked.

## PR instructions
- Title format: [<project_name>] <Title>
- Always run `pnpm lint` and `pnpm test` before committing.

## 模块注释与技术文档
- 每个模块（Go 包）必须在包入口或 `doc.go` 中说明职责、主要能力和边界；承担独立职责的实现文件也要有简短的文件头说明。Go 包注释以 `Package <包名>` 开头，命令入口可使用 `Command <命令名>`。模块说明优先使用中文，代码符号保留原名。
- 注释说明设计原因、关键约束及容易误用的行为；导出接口和关键方法应说明必要的输入、错误或并发约定，避免逐行复述代码。
- 技术文档负责整体架构、依赖关系、执行链路、API 和扩展方式，具体实现细节及其约束保留在代码附近。
- `docs/architecture.md` 必须维护模块导航，使用 Markdown 相对链接指向模块入口、关键实现和相关测试；模块注释注明对应文档位置，文档路径以仓库根目录为基准。优先链接文件并标明符号名，避免依赖容易失效的固定行号。
- 新增、移动模块或调整模块职责时，同步更新注释、技术文档和链接，确保说明与实现一致。
- 图的表达方式按场景分层，全部使用 Markdown，不引入 HTML 图册或需要构建的外部绘图文件：
  - 架构总览、模块协作和上下游关系图使用等宽字符框图：Markdown 放在 `text` 代码块中，Go 注释放在缩进代码块中；用方框标明模块名和职责，用带说明的箭头标明调用、读写或组装关系，并核对中文宽度与方框对齐。必要时另行说明返回路径，避免混淆运行调用与包导入依赖。
  - 单个流程、时序、状态和 ER 图使用 Mermaid 代码块；Mermaid 图附近保留一份等宽字符框图或文字说明，确保纯文本环境和 diff 中仍可读。
  - 复杂布局优先拆成多张 Mermaid 图或补充等宽框图，不新增独立图册文件。

## 计划记录与归档

- 全部计划统一放在仓库根目录的 `PLANs/`，每个计划使用独立 Markdown 文件；未完成计划放在 `PLANs/`，已完成计划移入 `PLANs/archive/`。目录约定见 [PLANs/README.md](PLANs/README.md)。
- 文件名使用 `YYYY-MM-DD-简短主题.md`，日期取可确认的创建日期；日期无法确认时使用 `undated-简短主题.md`，不得补造。同日不同计划通过主题区分；文件名保持稳定，完成时只移入归档目录，不因状态变化重命名。不要求每次新增或完成计划都更新公共索引，相关文档直接链接对应计划文件。
- 创建计划时记录创建时间和当前状态；时间统一使用北京时间（`Asia/Shanghai`），格式为 `YYYY-MM-DD HH:mm`。创建时间不因修改方案、调整顺序或归档而重置；历史计划只能确认日期时，仅记录 `YYYY-MM-DD` 并注明精度，无法确认时标记“未记录”，不得用提交时间或当前时间冒充。
- 只有计划目标实际完成且必要验证通过后，才标记完成并记录完成时间；待评估、部分完成或存在未完成验收项的计划保持未完成状态。
- 完成时先在原文件顶部（标题之后、原方案之前）添加“完成摘要”，记录已完成状态、原创建时间、完成时间，并用一段话概括目标、最终改动、关键边界、验证结果及必要的代码、文档或提交引用；再将整个文件移入 `PLANs/archive/`。原计划正文及实施记录完整保留，不用摘要替换、压缩或删除；当前状态以顶部完成摘要为准，下方原状态作为历史保留。部分完成或仍有未完成验收项的计划不得归档，也不因正文含“已完成”字样就归档。
- 归档时同步修正归档文件内部的相对链接，以及仓库中指向旧路径的引用，并验证链接目标存在。历史计划迁移沿用已有完成记录，不将归档时间当作创建时间或完成时间。
- 详细实现说明、字段对照和有长期价值的图表维护在对应技术文档中，计划通过 Markdown 相对链接引用。移动内容时同步修正引用，不使用容易因排序失效的“第几项计划”作为长期定位方式；迁移时只剩摘要的历史计划保留已有记录并注明，不补造原方案。

## 提交与署名

- 由 AI 参与者完成或参与完成的提交，在提交信息末尾添加署名 trailer：`Co-authored-by: <名称>`；一行一个，与正文之间空一行。
- 按实际参与者署名：Codex 参与写 `Co-authored-by: Codex`，DeepSeek Harness 参与写 `Co-authored-by: DeepSeek Harness`；双方都参与时两行都写。
- 没有可确认且已关联托管平台的邮箱时只写名称，不编造邮箱。无邮箱署名在 GitHub 上只作为文字显示，不会变成合作者；如需显示头像，必须使用已验证邮箱。
- 新提交直接添加；已提交但未推送的可用 `git commit --amend` 或交互式 rebase 补；已推送的历史需要重写时，先创建备份分支再改写，推送使用 `--force-with-lease`，并在完成后说明影响。
- 纯人工提交不强制署名；不要给未参与的提交补署名。
- 署名只描述协作来源，不替代代码评审、测试和计划归档。
