import { Component, type ReactNode } from "react";

// A deployment or interrupted download can leave a lazy chunk unavailable.
// Keep an explicit recovery action instead of an empty application root.
export class PageBoundary extends Component<{children:ReactNode}, {failed:boolean}> {
  state = {failed:false};
  static getDerivedStateFromError() { return {failed:true}; }
  render() {
    return this.state.failed ? <div className="data-panel" role="alert">
      <p>页面未能完整加载，可能是网络中断或版本已更新。</p>
      <p>刷新会丢弃未保存的页面编辑。</p>
      <button className="button" onClick={()=>location.reload()}>重新加载页面</button>
    </div> : this.props.children;
  }
}
