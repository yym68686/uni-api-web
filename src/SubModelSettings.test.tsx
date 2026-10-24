import { expect, it, vi } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { SubModelSettings } from "./SubModelSettings";
import { SUB_MODELS } from "./sub2apiModels";

it("selects and clears built-in and extra models independently", async () => {
 const save=vi.fn(),user=userEvent.setup();
 render(<SubModelSettings models={[SUB_MODELS[0],"extra-a"]} extraModels={["extra-a","extra-b",SUB_MODELS[0],"extra-b"]} onSave={save}/>);
 await user.click(screen.getByRole("button",{name:"检测模型设置"}));
 const dialog=within(screen.getByRole("dialog"));
 expect(dialog.getByRole("group",{name:"额外模型选择"})).toHaveTextContent("已选 1 / 2");
 await user.click(dialog.getByRole("button",{name:"全选内置模型"}));
 expect(dialog.getByRole("checkbox",{name:"extra-b"})).not.toBeChecked();
 expect(dialog.getByRole("checkbox",{name:"extra-a"})).toBeChecked();
 await user.click(dialog.getByRole("button",{name:"清空额外模型"}));
 expect(dialog.getByRole("checkbox",{name:SUB_MODELS[0]})).toBeChecked();
 expect(dialog.getByRole("checkbox",{name:"extra-a"})).not.toBeChecked();
 await user.click(dialog.getByRole("button",{name:"全选额外模型"}));
 await user.click(dialog.getByRole("button",{name:"清空内置模型"}));
 expect(dialog.getByRole("checkbox",{name:SUB_MODELS[0]})).not.toBeChecked();
 expect(dialog.getByRole("checkbox",{name:"extra-b"})).toBeChecked();
 await user.click(dialog.getByRole("button",{name:"保存设置"}));
 expect(save).toHaveBeenCalledExactlyOnceWith(["extra-a","extra-b"]);
});
