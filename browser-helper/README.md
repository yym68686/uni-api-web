# uni-api 登录助手

安装一次后，在 uni-api 控制台的「sub2api 检测」添加账号，填写站点网址、邮箱和密码。
如果直接登录遇到 Turnstile，点击「使用浏览器登录」。助手会请求该站点的访问权限，
打开原站、填写登录表单，再把成功登录的会话交回控制台。同步与检测由控制台继续执行。
强制人工验证或双因素验证需要在打开的原站页面完成，无需查找或复制 token。

## 安装

1. 打开 Chrome / Arc 的扩展管理页面，开启开发者模式。
2. 点击「加载已解压的扩展程序」，选择本目录。
3. 刷新 https://uni-api-console.fugue.pro。

只默认在 uni-api 控制台注入消息桥接；其他 HTTPS 站点按域名单独请求权限。
不申请 cookies、debugger、代理或历史记录权限。不修改 Turnstile，不伪造验证凭据。
密码只用于当前表单填写，不写入扩展存储。会话只返回发起登录的控制台页面，
由控制台服务端重新核验邮箱后加密保存。扩展不包含第三方上报服务。

若控制台部署在其他域名，修改 manifest.json 的 content_scripts.matches 及
background.js 的 consoleOrigin 后重新加载扩展。

本地开发或审核后执行 `npm run build`，会生成网页可下载的
`public/uni-api-browser-helper.zip`。源文件是安装包的唯一来源，不包含账号凭据。
