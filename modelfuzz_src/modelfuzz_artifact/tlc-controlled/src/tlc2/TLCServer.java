package tlc2;

import java.io.IOException;
import java.io.OutputStream;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.util.ArrayDeque;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.List;
import java.util.Queue;

import com.google.gson.Gson;
import com.sun.net.httpserver.*;

import tlc2.controlled.protocol.ActionMapper;
import tlc2.controlled.protocol.ActionMapperFactory;
import tlc2.controlled.protocol.ActionWrapper;
import tlc2.controlled.protocol.StateAbstractor;
import tlc2.controlled.protocol.StateAbstractorFactory;
import tlc2.output.EC;
import tlc2.output.MP;
import tlc2.tool.Action;
import tlc2.tool.ITool;
import tlc2.tool.StateVec;
import tlc2.tool.TLCState;
import tlc2.tool.impl.FastTool;
import tlc2.tool.impl.Tool;
import tlc2.value.impl.CounterExample;
import tlc2.util.RandomGenerator;
import tlc2.util.FP64;
import util.FileUtil;
import util.SimpleFilenameToStream;

// TLCServer 是给 ModelFuzz 使用的“可远程调用的 TLC 执行服务”。
//
// 这个类继承自 TLC，意思是它复用了 TLC 原本解析命令行参数、加载 TLA+ spec/config、
// 保存 mainFile/configFile/mapperParams 等配置的能力；在此基础上，它额外启动一个 HTTP
// server，让 Go 侧的 TLCClient 可以通过 POST /execute 提交 event trace。
//
// 整体数据流是：
//  1. ModelFuzz 的 Guider 把 event trace 作为 JSON 发到 /execute。
//  2. TLCServer 用 ActionMapper 把 JSON 事件翻译成 TLA+ 模型中的 Action。
//  3. TLCServer 从初始状态或上次保存的 currentState 开始，让 TLC 按这些 Action 推进状态。
//  4. StateAbstractor 可选地对状态序列做抽象，只保留覆盖统计真正关心的信息。
//  5. TLCServer 把状态字符串和 fingerprint key 以 JSON 返回给 Go 侧 Guider。
//
// 如果你还不熟 Java，可以把这个文件理解成一个普通程序：
//  - 字段保存 server 的运行状态。
//  - init/computeInitStates/simulate 是普通方法。
//  - main 是程序入口。
//  - HttpHandler 是 HTTP 请求到来时执行的回调函数。
public class TLCServer extends TLC {

    // ITool 是 TLC 内部用来操作 TLA+ 模型的核心接口。
    // 它可以加载模型 action、计算初始状态、根据某个 action 计算后继状态等。
    private ITool tool;

    // mapper 负责把外部 JSON event trace 翻译成 TLC 能执行的 ActionWrapper。
    // 不同协议有不同的 mapper，例如 RaftActionMapper、TPCActionMapper。
    private ActionMapper mapper;

    // abstractor 负责对 TLC 返回的具体状态序列做抽象。
    // 例如只保留模型覆盖关心的变量，或者把复杂状态压缩成更粗粒度的状态。
    private StateAbstractor abstractor;

    // currentState 保存“非 reset 模式”下上一次请求执行结束后的模型状态。
    // 如果每次请求都从初始状态开始，这个字段不会长期使用。
    private TLCState currentState;

    // 构造函数。Java 中构造函数名字必须和类名一样，没有返回值。
    // 这里只调用父类 TLC 的构造逻辑，没有额外初始化字段。
    public TLCServer() {
        super();
    }

    // init 在 main 解析完命令行参数之后调用，用来真正建立 TLC 执行环境。
    //
    // 这里做了四件关键事情：
    //  1. 创建 FastTool，让 TLC 能操作指定的 TLA+ 模型。
    //  2. 根据模型和参数选择 ActionMapper。
    //  3. 根据参数选择 StateAbstractor。
    //  4. 初始化 fingerprint 计算器，用于给状态生成稳定 key。
    public void init() {
        // FastTool 是 TLC 的模型执行工具。Tool.Mode.Simulation 表示这里不是完整 model checking，
        // 而是按照外部给定 action trace 做模拟执行。
        this.tool = new FastTool(mainFile, configFile, resolver, Tool.Mode.Simulation, params);

        // tool.getActions() 取出 TLA+ 模型中定义的所有 action。
        // ActionMapperFactory 会根据 mapperParams 和模型名选择具体 mapper。
        this.mapper = ActionMapperFactory.getMapper(this.mapperParams, Arrays.asList(this.tool.getActions()), this.tool.getRootName());

        // StateAbstractorFactory 根据参数选择状态抽象器。没有特殊配置时通常返回默认抽象器。
        this.abstractor = StateAbstractorFactory.getStateAbstractor(this.mapperParams);

        // currentState 初始为空。非 reset 模式第一次执行时，会从初始状态随机选一个作为起点。
        this.currentState = null;

        // 初始化 TLC 的 fingerprint 工具。fingerprint 可以理解成状态的哈希 key，
        // Go 侧 Guider 会用它判断“这个模型状态以前是否见过”。
        FP64.Init(fpIndex);
    }

    // simulate 是这个类最核心的方法：它把一段外部输入 trace 放进 TLA+ 模型里执行。
    //
    // 参数说明：
    //  - input：HTTP 请求体中的 JSON 字符串，也就是 ModelFuzz 发来的 event trace。
    //  - is_reset：是否每次请求都从模型初始状态重新开始。
    //
    // 返回值：
    //  - List<TLCState>：TLC 执行过程中访问到的模型状态序列。
    //
    // Java 里的 throws Exception 表示这个方法可能抛出错误，调用方需要处理。
    public List<TLCState> simulate(String input, boolean is_reset) throws Exception{
        // reset 模式：每个 /execute 请求都独立执行，从初始状态开始。
        // 这和 ModelFuzz 的一次 iteration 很匹配：每条 event trace 都对应一次完整模型重放。
        if (is_reset) {
            // 计算模型所有合法初始状态。
            StateVec initStates = computeInitStates(this.tool);

            // actionsToRun 是待执行 action 队列。Queue 表示先进先出。
            Queue<ActionWrapper> actionsToRun = new ArrayDeque<ActionWrapper>();

            // statesVisited 收集本次模拟访问到的状态，最后返回给 Guider 统计覆盖。
            List<TLCState> statesVisited = new ArrayList<TLCState>();

            // nextStates 用来临时保存执行一个 action 后得到的后继状态集合。
            StateVec nextStates = new StateVec(1);

            // 从初始状态集合中随机选一个作为本次模拟起点。
            // 如果模型有多个初始状态，这里不会枚举所有，而是选一个。
            TLCState curState = randomState(initStates);

            // 把初始状态也记录进状态轨迹。
            statesVisited.add(curState);

            // 把 JSON 输入转换成 ActionWrapper 列表，并放入待执行队列。
            actionsToRun.addAll(this.mapper.mapListOfActions(input));

            // reset 模式下循环直到遇到 Reset/Quit/Unknown action，或者无法继续执行。
            while(true) {
                // 每轮准备计算下一个非空后继状态集合。
                nextStates.clear();

                // 有些外部 action 在当前状态下可能不 enabled。
                // 如果执行后没有后继状态，就继续取下一个 action 尝试。
                while(nextStates.empty()) {
                    // 从队头取出下一个外部 action。
                    // remove() 在队列为空时会抛异常；当前代码假设输入 trace 最终包含 Reset/Quit。
                    ActionWrapper nextAction = actionsToRun.remove();

                    // Reset/Quit/Unknown 都表示本次模拟结束，返回已经访问到的状态序列。
                    if(nextAction.isReset() || nextAction.isQuit() || nextAction.action.equals(Action.UNKNOWN)) {
                        // 返回前做状态抽象，避免把完整 TLCState 暴露给上层。
                        return this.abstractor.doAbstraction(statesVisited);
                    }

                    // 让 TLC 从 curState 出发，执行指定 TLA+ action，计算后继状态。
                    nextStates = nextStates.addElements(tool.getNextStates(nextAction.action, curState));

                    // 如果该 action 在当前状态下不可执行，记录当前状态仍然停留不变。
                    if(nextStates.empty()) {
                        statesVisited.add(curState);
                    }
                }

                // 这里假设外部 action 对当前状态最多产生一个后继状态。
                // 换句话说，mapper 传入的 action 应该足够具体，不应导致多个可能后继。
                assert(nextStates.size() == 1);

                // 取出唯一后继状态。
                final TLCState s1 = nextStates.elementAt(0);
                if (s1 != null) {
                    // TLCState 可能携带延迟执行的 callable，这里执行它们完成状态计算。
                    s1.execCallable();
                    // 当前状态推进到后继状态。
                    curState = s1;
                }
                // 记录推进后的状态。
                statesVisited.add(curState);
            }
        } else {
            // 非 reset 模式：多个 /execute 请求共享 currentState。
            // 下一次请求会从上一次请求结束的模型状态继续执行。
            if (this.currentState == null)
                // 第一次请求没有历史状态时，从初始状态中随机选一个。
                this.currentState = randomState(computeInitStates(this.tool));

            // curState 是本次请求的局部当前状态，初始值来自 currentState。
            TLCState curState = this.currentState;

            // nextStates 保存每个 action 的后继状态集合。
            StateVec nextStates = new StateVec(1);

            // actionsToRun 保存本次请求中要执行的外部 action。
            Queue<ActionWrapper> actionsToRun = new ArrayDeque<ActionWrapper>();

            // statesVisited 记录本次请求期间访问到的状态。
            List<TLCState> statesVisited = new ArrayList<TLCState>();
            // 先记录请求开始时的状态。
            statesVisited.add(curState);

            // 把 JSON 输入转成 ActionWrapper 队列。
            actionsToRun.addAll(this.mapper.mapListOfActions(input));
            
            // 非 reset 模式只执行本次请求里给出的 action；队列空了就结束。
            while(actionsToRun.size() > 0) {
                // 取出下一个 action。
                ActionWrapper nextAction = actionsToRun.remove();

                // 遇到 Reset/Quit/Unknown 时，清空 currentState，让下一次请求重新从初始状态开始。
                if(nextAction.isReset() || nextAction.isQuit() || nextAction.action.equals(Action.UNKNOWN)) {
                    this.currentState = null;
                    return this.abstractor.doAbstraction(statesVisited);
                }

                // 计算执行该 action 后的后继状态。
                nextStates = nextStates.addElements(tool.getNextStates(nextAction.action, curState));

                // 如果该 action 当前不可执行，就记录状态没有变化。
                if(nextStates.empty()) {
                    statesVisited.add(curState);
                }
                
                // assert(nextStates.size() == 1);
                // 当前代码没有强制检查后继状态数量，只取第一个。
                // 这意味着如果 action 在模型中有多个后继，这里会忽略其余后继。
                final TLCState s1 = nextStates.elementAt(0);
                if (s1 != null) {
                    // 完成 TLCState 内部可能延迟的计算。
                    s1.execCallable();
                    // 推进当前状态。
                    curState = s1;
                }
                // 记录推进后的状态。
                statesVisited.add(curState);
            }

            // 请求正常执行完后，把最终状态保存下来，供下一次非 reset 请求继续使用。
            this.currentState = curState;

            // 返回抽象后的状态轨迹。
            return this.abstractor.doAbstraction(statesVisited);
        }
    }

    // randomState 从一个 TLC 状态集合中随机挑选一个状态。
    //
    // StateVec 是 TLC 自己的状态列表类型；TLCState 是 TLC 表示“一个模型状态”的对象。
    // 如果集合为空，返回 null。
    private TLCState randomState(StateVec states) {
        // 创建 TLC 提供的随机数生成器。
        RandomGenerator gen = new RandomGenerator();
        // len 是可选状态数量。
        final int len = states.size();
		if (len > 0) {
            // gen.nextDouble() 返回 [0, 1) 之间的小数，乘以 len 后取 floor 得到合法下标。
			final int index = (int) Math.floor(gen.nextDouble() * len);
            // 返回随机下标对应的状态。
			return states.elementAt(index);
		}
        // 没有任何状态可选时返回空。
		return null;
    }

    // computeInitStates 计算并检查 TLA+ 模型的初始状态集合。
    //
    // 它不仅调用 TLC 生成 init states，还会：
    //  - 检查模型 assumptions。
    //  - 检查每个初始状态是否完整。
    //  - 检查初始状态是否满足 invariants。
    //  - 过滤掉不在 model constraint 中的状态。
    //
    // 返回值 StateVec 是经过 deepNormalize 的合法初始状态集合。
    public StateVec computeInitStates(ITool tool) throws Exception{
        // checkAssumptions 用来检查 TLA+ config/spec 中的 ASSUME 是否成立。
        final int res = tool.checkAssumptions();
		if (res != EC.NO_ERROR) {
            // 非 NO_ERROR 表示 assumption 检查失败，直接中止模拟。
			throw new Exception("Error checking assumptions: "+res);
		}
		
        // curState 记录当前正在检查的初始状态；出错时用于生成更有上下文的错误信息。
		TLCState curState = null;
        // initStates 保存最终通过检查的初始状态。
        StateVec initStates;

		//
		// Compute the initial states.
		//
		try {
			// The init states are calculated only ever once and never change
			// in the loops below. Ideally the variable would be final.
            // 调用 TLC 计算所有候选初始状态。
			final StateVec inits = tool.getInitStates();
            // 创建一个新的 StateVec，用来保存通过检查且在模型约束内的初始状态。
			initStates = new StateVec(inits.size());
			
            // 取出模型中定义的 invariants。
            Action[] invariants = tool.getInvariants();
			// Check all initial states for validity.
			for (int i = 0; i < inits.size(); i++) {
                // 逐个检查候选初始状态。
				curState = inits.elementAt(i);
                // isGoodState 检查状态是否完整指定了所有变量。
				if (tool.isGoodState(curState)) {
					for (int j = 0; j < invariants.length; j++) {
                        // 初始状态也必须满足所有 invariant。
						if (!tool.isValid(invariants[j], curState)) {
							// We get here because of invariant violation.
                            // 构造 TLC 风格的 invariant violation 错误信息。
                            String errorMessage = MP.getError(EC.TLC_INVARIANT_VIOLATED_INITIAL,
									new String[] { tool.getInvNames()[j], tool.evalAlias(curState, curState).toString() });
                            // 记录 counterexample，方便 TLC 内部错误报告。
							tool.checkPostConditionWithCounterExample(new CounterExample(curState));
                            throw new Exception(errorMessage);
							
						}
					}
				} else {
                    // 如果初始状态没有完整指定变量，就抛出 TLC 标准错误。
					throw new Exception(MP.getError(EC.TLC_STATE_NOT_COMPLETELY_SPECIFIED_INITIAL, curState.toString()));
				}
				
                // isInModel 用于检查 model constraint。只有在模型约束内的状态才保留。
				if (tool.isInModel(curState)) {
					initStates.addElement(curState);
				}
			}
		} catch (Exception e) {
            
            // 这里把底层异常包装成更接近 TLC 输出风格的错误信息。
			final String errorMessage;
			if (curState != null) {
                // 如果已经知道是哪个初始状态出错，就把该状态也放进错误信息。
				errorMessage = MP.getError(EC.TLC_INITIAL_STATE,
						new String[] { (e.getMessage() == null) ? e.toString() : e.getMessage(), curState.toString() });
			} else {
                // 如果还没有具体状态，就直接使用原始错误信息。
				errorMessage = e.getMessage(); // LL changed call 7 April 2012
			}
			throw new Exception(errorMessage);
		}

		// It appears deepNormalize brings the states into a canonical form to
		// speed up equality checks.
        // deepNormalize 可以理解为把状态整理成规范形式，便于后续比较、hash 和 fingerprint。
		initStates.deepNormalize();
        return initStates;
    }

    // ServerResponse 是 /execute 返回给 Go 侧 TLCClient 的 JSON 结构。
    //
    // static class 表示这是 TLCServer 内部定义的辅助类，不依赖某个具体 TLCServer 对象。
    // Gson 会把这个对象序列化成类似下面的 JSON：
    //
    // {
    //   "states": ["...state repr...", "..."],
    //   "keys": [12345, 67890]
    // }
    public static class ServerResponse {
        // states 保存每个 TLCState 的字符串表示，主要用于记录和调试。
        public List<String> states;

        // keys 保存每个状态的 fingerprint，Go 侧 Guider 用它做唯一状态覆盖统计。
        public List<Long> keys;

        // 构造函数：创建响应对象时同时填入 states 和 keys。
        public ServerResponse(List<String> states, List<Long> keys) {
            this.states = states;
            this.keys = keys;
        }
    }

    // main 是 Java 程序入口。运行 TLCServer.java 对应程序时，JVM 会从这里开始执行。
    //
    // 它负责：
    //  1. 解析 TLC 命令行参数。
    //  2. 加载 TLA+ spec/config。
    //  3. 初始化 TLCServer。
    //  4. 启动 HTTP server，提供 /health 和 /execute 两个接口。
    public static void main(String[] args) throws Exception {
        System.out.println("Initializing...");
        // 创建 TLCServer 对象。final 表示这个变量后面不会再指向另一个对象。
        final TLCServer tlcServer = new TLCServer();

        // handleParameters 是父类 TLC 提供的方法，用于解析命令行参数。
        // 如果参数非法，返回 false 并退出程序。
        if (!tlcServer.handleParameters(args)) {
            System.exit(1);
        }

        // 从 mainFile 中解析目录，用于让 TLC 找到 spec/config 引用的其他文件。
        final String dir = FileUtil.parseDirname(tlcServer.getMainFile());
        if (!dir.isEmpty()) {
            // 如果 spec 文件带目录，就以该目录为基础解析文件名。
            tlcServer.setResolver(new SimpleFilenameToStream(dir));
        } else {
            // 否则使用默认当前目录解析文件。
            tlcServer.setResolver(new SimpleFilenameToStream());
        }
        // 初始化 FastTool、ActionMapper、StateAbstractor 等核心组件。
        tlcServer.init();

        // 默认 HTTP 端口是 2023。
        int serverPort = 2023;
        // mapperParams 是 TLC 参数中传给 mapper 的键值参数。
        // 如果其中配置了 port，就用它覆盖默认端口。
        if (tlcServer.mapperParams.containsKey("port"))
            serverPort = Integer.parseInt(tlcServer.mapperParams.get("port"));

        // 额外扫描命令行参数，支持通过 -serverport 指定 HTTP server 端口。
        int index = 0;
		while (index < args.length) {
            if (args[index].equals("-serverport")) {
                index++;
                if (index < args.length) {
                    try {
                        // 把 -serverport 后面的字符串解析成整数。
                        serverPort = Integer.parseInt(args[index]);
                    } catch (NumberFormatException e) {
                        // 如果不是数字，就输出 TLC 风格的命令行错误并退出。
                        MP.printError(EC.WRONG_COMMANDLINE_PARAMS_TLC, "server port should be a number");
                        System.exit(1);;
                    }
                }
            }
            index++;
        }
        try {
            // 创建 JDK 自带的轻量 HTTP server。
            // new InetSocketAddress(serverPort) 表示监听本机的 serverPort 端口。
            // 第二个参数 0 表示使用默认连接 backlog。
            HttpServer httpServer = HttpServer.create(new InetSocketAddress(serverPort), 0);

            // 注册 /health 接口。它用于外部检查 server 是否还活着。
            httpServer.createContext("/health", new HttpHandler() {
                // handle 是请求到达 /health 时执行的方法。
                public void handle(HttpExchange t) throws IOException {
                    // 返回简单字符串 Ok。
                    String response = "Ok";
                    // 发送 HTTP 200 响应头，并告诉客户端响应体长度。
                    t.sendResponseHeaders(200, response.length());
                    // 获取响应输出流，后面往里面写字节。
                    OutputStream responseStream = t.getResponseBody();
                    // 写入响应内容。
                    responseStream.write(response.getBytes());
                    // 关闭流，表示响应发送完毕。
                    responseStream.close();
                }
            });

            // 注册 /execute 接口。ModelFuzz 的 TLCClient.SendTrace 会 POST 到这里。
            httpServer.createContext("/execute", new HttpHandler() {
                // handle 是请求到达 /execute 时执行的方法。
                public void handle(HttpExchange t) throws IOException {
                    // /execute 只接受 POST，因为请求体中要携带 event trace JSON。
                    if(!t.getRequestMethod().equalsIgnoreCase("POST")) {
                        // 非 POST 请求返回 405 Method Not Allowed。
                        t.sendResponseHeaders(405, -1);
                        return;
                    }
                    try {
                        // 读取完整请求体字节。
                        byte[] requestBytes = t.getRequestBody().readAllBytes();
                        // 按 UTF-8 转成字符串，这就是 Go 侧发来的 event trace JSON。
                        String request = new String(requestBytes, StandardCharsets.UTF_8);
                        // 默认 reset 模式：每次 /execute 都从模型初始状态开始模拟。
                        boolean is_reset = true;
                        // 如果 mapperParams 中包含 not_reset，则改为连续模式，沿用 currentState。
                        if (tlcServer.mapperParams.containsKey("not_reset"))
                            is_reset = false;
                            
                        // 执行模型模拟：JSON event trace -> TLA+ actions -> TLC states。
                        List<TLCState> trace = tlcServer.simulate(request, is_reset);

                        // stringTrace 保存每个状态的字符串形式。
                        List<String> stringTrace = new ArrayList<>();
                        // fingerprintTrace 保存每个状态的 fingerprint key。
                        List<Long> fingerprintTrace = new ArrayList<>();
                        for( TLCState state : trace) {
                            // state.toString() 是人类可读的状态表示。
                            stringTrace.add(state.toString());
                            // fingerPrint() 是 TLC 用于状态去重的长整数 key。
                            fingerprintTrace.add(state.fingerPrint());
                        }
                        // Gson 是 JSON 序列化库，用来把 Java 对象变成 JSON 字符串。
                        Gson gson = new Gson();
                        // 构造响应对象并序列化。
                        String response = gson.toJson(new ServerResponse(stringTrace, fingerprintTrace));
                        // 返回 HTTP 200，响应体是 JSON。
                        t.sendResponseHeaders(200, response.length());
                        // 写入响应体。
                        OutputStream responseStream = t.getResponseBody();
                        responseStream.write(response.getBytes());
                        responseStream.close();
                    } catch (Exception e) {
                        // 模拟执行、action 映射、TLC 状态计算等任何异常都会返回 500。
                        String errorMessage = e.getMessage();
                        // 发送 HTTP 500，并把错误信息作为响应体返回给客户端。
                        t.sendResponseHeaders(500, errorMessage.length());
                        OutputStream response = t.getResponseBody();
                        response.write(errorMessage.getBytes());
                        response.close();
                    }
                }
            });
            // setExecutor(null) 表示使用 HttpServer 默认 executor。
            // 简单理解：让 HTTP server 自己决定如何调度请求处理线程。
            httpServer.setExecutor(null);
            System.out.println("Server starts listening on port: "+Integer.toString(serverPort));
            // 真正启动 HTTP server，从这一行之后开始接收请求。
            httpServer.start();
        } catch (Exception e) {
            // server 启动失败时打印错误，例如端口被占用。
            System.out.println("Error running server: "+e.getMessage());
        }
    }
}
