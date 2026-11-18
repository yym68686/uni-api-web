import { Component, type ReactNode } from "react";

// A deployment or interrupted download can leave a lazy chunk unavailable.
// Keep an explicit recovery action instead of an empty application root.
export class PageBoundary extends Component<{children:ReactNode; onDismiss?:()=>void}, {failed:boolean}> {
  state = {failed:false};
  static getDerivedStateFromError() { return {failed:true}; }
  render() {
    return this.state.failed ? <div className="data-panel" role="alert">
      <p>页面未能完整加载，可能是网络中断或版本已更新。</p>
      {this.props.onDismiss ? <button className="button" onClick={this.props.onDismiss}>关闭并返回工作区</button> :
        <>
          <button className="button" onClick={()=>this.setState({failed:false})}>重试本栏目</button>
          <p>如果重试无效，可以重新加载；刷新会丢弃未保存的页面编辑。</p>
          <button className="button" onClick={()=>location.reload()}>重新加载页面</button>
        </>}
    </div> : this.props.children;
  }
}
