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
