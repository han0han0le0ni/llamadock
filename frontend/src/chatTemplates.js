// 内置 Chat 模板库
//
// 这些模板经 llama.cpp 的 minja 引擎验证，语法精简、兼容性强。
// 选择后写入 chat_template，并把 chat_template_mode 设为 jinja（auto 会清空改用模型自带模板）。
// 说明：不同模型对模板细节敏感，内置模板是「能用的通用写法」，不保证与模型原训练模板逐字一致。

export const chatTemplates = [
  {
    id: 'auto',
    label: 'auto · 用模型自带模板（推荐）',
    desc: '清空自定义模板，让 llama-server 读取 GGUF 内嵌的 tokenizer.chat_template。绝大多数模型选这个即可。',
    mode: 'auto',
    text: '',
  },
  {
    id: 'loose',
    label: '通用宽松 · 报错最少（兜底推荐）',
    desc: '不校验角色顺序、不依赖特殊 token、任何角色都兜底。当模型自带模板报「System message must be at the beginning」等严格校验错误时，用它接管。格式不如专用模板精致，但最不容易报错。',
    mode: 'jinja',
    text: `{% for message in messages %}{% if message['role'] == 'system' %}### System:\n{{ message['content'] }}\n\n{% elif message['role'] == 'user' %}### User:\n{{ message['content'] }}\n\n{% elif message['role'] == 'assistant' %}### Assistant:\n{{ message['content'] }}\n\n{% else %}{{ message['role'] }}:\n{{ message['content'] }}\n\n{% endif %}{% endfor %}### Assistant:\n`,
  },
  {
    id: 'chatml',
    label: 'ChatML · 通用 / OpenChat',
    desc: '适用于 ChatML 训练的模型（Qwen1.5、OpenChat、部分微调模型）。',
    mode: 'jinja',
    text: `{% for message in messages %}{{'<|im_start|>' + message['role'] + '\n' + message['content'] + '<|im_end|>' + '\n'}}{% endfor %}{% if add_generation_prompt %}{{ '<|im_start|>assistant\n' }}{% endif %}`,
  },
  {
    id: 'llama3',
    label: 'Llama 3 / 3.1',
    desc: 'Meta Llama 3 系列格式。注意：此模板要求 system 在首位，网关已自动归一化消息顺序。',
    mode: 'jinja',
    text: `{% for message in messages %}{% if loop.first and message['role'] != 'system' %}{{ '<|start_header_id|>system<|end_header_id|>\n\n<|eot_id|>' }}{% endif %}{{ '<|start_header_id|>' + message['role'] + '<|end_header_id|>\n\n' + message['content'] + '<|eot_id|>' }}{% endfor %}{% if add_generation_prompt %}{{ '<|start_header_id|>assistant<|end_header_id|>\n\n' }}{% endif %}`,
  },
  {
    id: 'qwen25',
    label: 'Qwen 2.5',
    desc: '通义千问 2.5 系列（含 instruct）。同样要求 system 在首位。',
    mode: 'jinja',
    text: `{% for message in messages %}{% if loop.first and message['role'] != 'system' %}{{ '<|im_start|>system\nYou are a helpful assistant.<|im_end|>\n' }}{% endif %}{{ '<|im_start|>' + message['role'] + '\n' + message['content'] + '<|im_end|>\n' }}{% endfor %}{% if add_generation_prompt %}{{ '<|im_start|>assistant\n' }}{% endif %}`,
  },
  {
    id: 'gemma',
    label: 'Gemma / Gemma 2',
    desc: 'Google Gemma 系列指令模型格式。',
    mode: 'jinja',
    text: `{% for message in messages %}{% if message['role'] == 'user' %}{{ '<start_of_turn>user\n' + message['content'] + '<end_of_turn>\n' }}{% elif message['role'] == 'assistant' %}{{ '<start_of_turn>model\n' + message['content'] + '<end_of_turn>\n' }}{% endif %}{% endfor %}{% if add_generation_prompt %}{{ '<start_of_turn>model\n' }}{% endif %}`,
  },
  {
    id: 'alpaca',
    label: 'Alpaca / Vicuna 简易式',
    desc: '老式 instruct 模型，无角色标记，仅拼指令与回答。兼容性最好。',
    mode: 'manual',
    text: `Below is an instruction that describes a task. Write a response that appropriately completes the request.\n\n### Instruction:\n{{.Prompt}}\n\n### Response:\n`,
  },
]

export function findTemplate(id) {
  return chatTemplates.find((t) => t.id === id) || null
}
