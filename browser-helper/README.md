# uni-api 登录助手

安装后，在 uni-api 控制台的「渠道管理」添加账号，填写站点网址、用户名/邮箱和密码。
如果直接登录遇到 Turnstile，点击「使用浏览器登录」。助手直接打开原站、自动勾选登录协议并填写登录表单，再把成功登录的会话交回控制台。同步与检测由控制台继续执行。
强制人工验证或双因素验证需要在打开的原站页面完成，无需查找或复制 token。

New API（包括 FoxRouter）需登录助手 1.3.0 或更新版本。更新已解压的扩展后，
在扩展管理页点击重新加载，并刷新控制台。首次点击扩展图标，选择「授权 New API 会话读取」。
随后在控制台选择「使用浏览器登录」，在原站完成人机验证并手动点击登录；原站登录协议由用户自行确认。
助手只在这次登录期间读取当前站点的 `session` 和 `new_api_refresh` Cookie，
校验原站身份后将会话交给控制台，由后台再次验证并加密保存。不会读取 `cf_clearance`。
兼容旧版 Cookie 会话及新版访问令牌/刷新会话；可刷新的会话由后台自动续期，
过期或撤销且需要再次验证时，会提示重新使用浏览器登录。

## Chrome / Chromium 安装（121+）

1. 下载 `uni-api-browser-helper-chrome.zip` 并解压，或直接使用本源码目录。
2. 打开 `chrome://extensions`（Arc / Edge 使用各自的扩展管理页），开启开发者模式。
3. 点击「加载已解压的扩展程序」，选择包含 `manifest.json` 的目录。
4. 刷新 https://uni-api-console.fugue.pro。

## Firefox 安装（桌面版 140+）

1. 下载 `uni-api-browser-helper-firefox.zip` 并解压，或直接使用本源码目录。
2. 打开 `about:debugging#/runtime/this-firefox`，点击「临时载入附加组件」。
3. 选择解压目录中的 `manifest.json`，接受所需的站点访问和数据传输授权。
4. 刷新 https://uni-api-console.fugue.pro。

临时扩展在 Firefox 重启后失效，需重新加载。正式版 Firefox 长期安装必须先通过
Mozilla AMO 获得签名（可选择不公开列出的自分发模式），再安装签名后的 `.xpi`；
生成的 ZIP **未签名**，重命名为 XPI 不会绕过签名要求。本仓库不自动上传或发布扩展。

Firefox 使用事件后台脚本，Chrome 使用 Service Worker；源码 manifest 同时声明两种
后台，打包时分别裁剪为各浏览器原生格式。运行时优先使用 `browser` 的 Promise API，
否则使用 Chrome MV3 的 `chrome` Promise API，不依赖远程脚本或第三方 polyfill。
Firefox 包声明会传递认证信息、账号身份及站点会话 Cookie，用于明确告知用户
登录会话将交给控制台，而不是虚报“不收集数据”。

## 权限与会话边界

安装时授予 HTTPS 站点访问权限，登录时不再打开授权中间页。
若浏览器收回访问权限，在扩展管理页重新允许访问控制台及目标 HTTPS 站点。
消息桥接仅在 uni-api 控制台运行；只有用户发起登录时才操作本次目标站点。
Cookie 为 New API 专用的可选权限，用户明确授权后启用；sub2api 不需要此权限。
New API 在 Firefox 中沿用发起控制台的容器 Cookie Store，并只读取目标登录标签页
所在的存储；不跨普通窗口、隐私窗口或容器取会话。不导出分区或第一方隔离 Cookie。
使用隐私窗口时，还需在浏览器设置中明确允许该扩展在隐私窗口运行。
若 Firefox 未提供发起标签页元数据，助手仍校验浏览器提供的控制台页面 URL 与来源，
但新登录页会在默认窗口打开，且完成后不会强制切回控制台标签页。
不申请 debugger、代理或历史记录权限。不修改 Turnstile，不伪造验证凭据。
密码只用于当前表单填写，不写入扩展存储。会话只返回发起登录的控制台页面，
由控制台服务端重新核验邮箱后加密保存。扩展不包含第三方上报服务。

若控制台部署在其他域名，修改 manifest.json 的 content_scripts.matches 及
background.js 的 consoleOrigin 后重新加载扩展。

## 打包与验证

执行 `npm run package:browser-helper`（`npm run build` 也会自动执行），生成：

- `public/uni-api-browser-helper-chrome.zip`：Chrome 专用包。
- `public/uni-api-browser-helper-firefox.zip`：Firefox 专用包，可用于临时加载及后续签名提交。
- `public/uni-api-browser-helper.zip`：保留旧下载地址的通用包。

ZIP 根目录直接包含 `manifest.json`；源文件是安装包的唯一来源，只收录审核过的
运行文件和本说明，不包含测试文件、账号凭据、工作目录或依赖库。
修改源码后需重新打包、重新加载扩展并刷新控制台。

运行 `npm test -- browser-helper scripts/package-browser-helper.test.js` 验证两种
API 命名空间、消息来源限制、取消登录、Cookie 权限与隔离以及三种安装包。
两种后台的兼容依据参见 [MDN 后台文档](https://developer.mozilla.org/en-US/docs/Mozilla/Add-ons/WebExtensions/manifest.json/background)，
Firefox 授权字段参见 [Mozilla 数据传输授权说明](https://extensionworkshop.com/documentation/develop/firefox-builtin-data-consent/)。

真实 Firefox 的隔离冒烟验证（需本机 Firefox、geckodriver 和 OpenSSL）：

```sh
npm run package:browser-helper
node scripts/verify-browser-helper-firefox.mjs /path/to/firefox /path/to/geckodriver
```

脚本使用临时配置目录、真实 Firefox 无头实例和本地 HTTPS 测试站点，验证 sub2api
登录、取消登录、New API 手动提交及容器 Cookie 隔离；代理仅连接本地测试服务，
不会访问真实控制台或使用真实账号。New API 的可选 Cookie 权限由测试环境预先授予，
授权弹窗仍需单独人工验证。运行结果和诊断保存在输出的临时目录，不写入仓库。
