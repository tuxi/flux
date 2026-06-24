# Flux

Type A（control loop）
```plain text
Goal
 ↓
LLM Planner
 ↓
PlanSource.Next()
 ↓
Scheduler
 ↓
Tool
 ↓
Observation
 ↓
LLM Planner
```

Type B（dataflow DAG）
```plain text
Goal
 ↓
LLM
 ↓
DAG
 ↓
Compile
 ↓
Kernel
 ↓
Data flow
 ↓
Done
```