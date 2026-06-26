---
name: list_files
description: "List files in a directory. Use when you need to see what files exist."
implementation: tool
tool: shell
inputs:
  directory:
    type: string
    description: "Directory path to list (default is current directory)"
    required: false
---
