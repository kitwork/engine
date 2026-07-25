// Kitwork client kernel.
//
// One root:
//   window.kitwork
//
// One expression runtime.
// One component registry.
// One delegated event system.
// One DOM observer.
//
// Author source:
//   data-kit-*
//
// Engine-emitted IR:
//   data-kitwork-*
//
// No eval.
// No new Function.
(function (window, document) {
    "use strict";

    var kitwork = window.kitwork || (window.kitwork = {});

    if (kitwork.runtime && kitwork.runtime.booted) {
        return;
    }

    kitwork.runtime = {
        name: "kitwork",
        version: "1.0.0",
        engine: "web",
        development: false,
        booted: true,

        info: function () {
            return {
                name: this.name,
                version: this.version,
                engine: this.engine,
                development: this.development
            };
        }
    };

    // ---------------------------------------------------------------------------
    // Registries and private state
    // ---------------------------------------------------------------------------

    var modules = Object.create(null);
    var behaviors = Object.create(null);
    var blueprints = Object.create(null);
    var aliases = Object.create(null);
    var streams = Object.create(null);
    var directiveCache = Object.create(null);
    var raw = Object.create(null);
    var globalCleanups = new Set();
    var stateKey = Symbol("kitwork");

    var renderScheduled = false;
    var syncScheduled = false;
    var callDepth = 0;

    var MODEL =
        "[data-kitwork-model],[data-kit-model]";

    var SCOPE =
        "[data-kitwork-scope],[data-kit-scope]," +
        "[data-kitwork-component],[data-kit-component]," +
        "[data-kitwork-api],[data-kit-api]";

    var ACTION =
        "[data-kitwork-action],[data-kit-action]";

    var LIVE =
        "[data-kitwork-live],[data-kit-live]";

    var API =
        "[data-kitwork-api],[data-kit-api]";

    var REMEMBER =
        "[data-kitwork-remember],[data-kit-remember]";

    var VISIBLE =
        '[data-kitwork-trigger="visible"],[data-kit-trigger="visible"]';

    // ---------------------------------------------------------------------------
    // Module registry
    // ---------------------------------------------------------------------------

    kitwork.module = function (name, value) {
        if (
            typeof name !== "string" ||
            name.trim() === ""
        ) {
            throw new TypeError(
                "kitwork.module: name is required"
            );
        }

        name = name.trim();

        if (arguments.length === 1) {
            return modules[name] || null;
        }

        if (modules[name]) {
            return modules[name];
        }

        modules[name] = value;
        kitwork[name] = value;

        return value;
    };

    kitwork.has = function (name) {
        return !!modules[name];
    };

    kitwork.modules = function () {
        return Object.keys(modules);
    };

    // ---------------------------------------------------------------------------
    // Expression lexer and parser
    // ---------------------------------------------------------------------------

    var PREC = {
        "||": 1,
        "&&": 2,
        "==": 3,
        "!=": 3,
        ">": 4,
        "<": 4,
        ">=": 4,
        "<=": 4,
        "+": 5,
        "-": 5,
        "*": 6,
        "/": 6,
        "%": 6
    };

    function lex(source) {
        var tokens = [];
        var index = 0;
        var length = source.length;

        while (index < length) {
            var char = source[index];

            if (
                char === " " ||
                char === "\t" ||
                char === "\n" ||
                char === "\r"
            ) {
                index++;
                continue;
            }

            if (
                (char >= "0" && char <= "9") ||
                (
                    char === "." &&
                    index + 1 < length &&
                    source[index + 1] >= "0" &&
                    source[index + 1] <= "9"
                )
            ) {
                var numberEnd = index;

                while (
                    numberEnd < length &&
                    (
                        (
                            source[numberEnd] >= "0" &&
                            source[numberEnd] <= "9"
                        ) ||
                        source[numberEnd] === "."
                    )
                ) {
                    numberEnd++;
                }

                tokens.push({
                    t: "num",
                    v: source.slice(index, numberEnd)
                });

                index = numberEnd;
                continue;
            }

            if (char === "'" || char === '"') {
                var quote = char;
                var stringEnd = index + 1;
                var value = "";

                while (stringEnd < length) {
                    var current = source[stringEnd];

                    if (current === "\\") {
                        stringEnd++;

                        if (stringEnd >= length) {
                            break;
                        }

                        var escaped = source[stringEnd];

                        if (escaped === "n") value += "\n";
                        else if (escaped === "r") value += "\r";
                        else if (escaped === "t") value += "\t";
                        else value += escaped;

                        stringEnd++;
                        continue;
                    }

                    if (current === quote) {
                        break;
                    }

                    value += current;
                    stringEnd++;
                }

                if (stringEnd >= length) {
                    throw new Error(
                        "kitwork: unterminated string"
                    );
                }

                tokens.push({
                    t: "str",
                    v: value
                });

                index = stringEnd + 1;
                continue;
            }

            if (/[A-Za-z_$]/.test(char)) {
                var nameEnd = index;

                while (
                    nameEnd < length &&
                    /[A-Za-z0-9_$]/.test(source[nameEnd])
                ) {
                    nameEnd++;
                }

                tokens.push({
                    t: "id",
                    v: source.slice(index, nameEnd)
                });

                index = nameEnd;
                continue;
            }

            var pair = source.slice(index, index + 2);

            if (
                pair === "==" ||
                pair === "!=" ||
                pair === ">=" ||
                pair === "<=" ||
                pair === "&&" ||
                pair === "||" ||
                pair === "=>"
            ) {
                tokens.push({
                    t: "op",
                    v: pair
                });

                index += 2;
                continue;
            }

            if (
                "+-*/%<>!?:().,={}[];".indexOf(char) >= 0
            ) {
                tokens.push({
                    t: "op",
                    v: char
                });
            }

            index++;
        }

        tokens.push({
            t: "eof",
            v: ""
        });

        return tokens;
    }

    function parse(tokens) {
        var position = 0;

        function peek() {
            return tokens[position];
        }

        function next() {
            return tokens[position++];
        }

        function eat(value) {
            if (peek().v !== value) {
                throw new Error(
                    "kitwork: expected " + value
                );
            }

            next();
        }

        function assignment() {
            var left = ternary();

            if (peek().v === "=") {
                next();

                var value = assignment();

                if (
                    left instanceof Array &&
                    left[0] === "$" &&
                    left[1] !== "$"
                ) {
                    return ["=", left[1], value];
                }

                if (
                    left instanceof Array &&
                    left[0] === "." &&
                    left[1] instanceof Array &&
                    left[1][0] === "$" &&
                    left[1][1] === "$"
                ) {
                    return ["=$", left[2], value];
                }

                throw new Error(
                    "kitwork: invalid assignment"
                );
            }

            return left;
        }

        function ternary() {
            var condition = binary(0);

            if (peek().v === "?") {
                next();

                var yes = assignment();

                eat(":");

                var no = assignment();

                return [
                    "?",
                    condition,
                    yes,
                    no
                ];
            }

            return condition;
        }

        function binary(minimum) {
            var left = unary();

            for (; ;) {
                var token = peek();

                if (
                    token.t !== "op" ||
                    !(token.v in PREC) ||
                    PREC[token.v] < minimum
                ) {
                    break;
                }

                var operator = next().v;

                left = [
                    operator,
                    left,
                    binary(PREC[operator] + 1)
                ];
            }

            return left;
        }

        function unary() {
            var value = peek().v;

            if (value === "!" || value === "-") {
                next();

                return [
                    "u" + value,
                    unary()
                ];
            }

            return postfix();
        }

        function callArguments() {
            var argumentsList = [];

            if (peek().v !== ")") {
                argumentsList.push(assignment());

                while (peek().v === ",") {
                    next();
                    argumentsList.push(assignment());
                }
            }

            eat(")");

            return argumentsList;
        }

        function postfix() {
            var expression = primary();

            for (; ;) {
                if (peek().v === ".") {
                    next();

                    var name = next();

                    if (name.t !== "id") {
                        throw new Error(
                            "kitwork: expected member name"
                        );
                    }

                    if (peek().v === "(") {
                        next();

                        expression = [
                            "()",
                            expression,
                            name.v,
                            callArguments()
                        ];
                    } else {
                        expression = [
                            ".",
                            expression,
                            name.v
                        ];
                    }

                    continue;
                }

                if (peek().v === "(") {
                    next();

                    expression = [
                        "call",
                        expression,
                        callArguments()
                    ];

                    continue;
                }

                break;
            }

            return expression;
        }

        function tryArrowParameters() {
            var saved = position;

            next();

            var parameters = [];

            if (peek().v === ")") {
                next();
            } else {
                for (; ;) {
                    if (peek().t !== "id") {
                        position = saved;
                        return null;
                    }

                    parameters.push(next().v);

                    if (peek().v === ",") {
                        next();
                        continue;
                    }

                    break;
                }

                if (peek().v !== ")") {
                    position = saved;
                    return null;
                }

                next();
            }

            if (peek().v !== "=>") {
                position = saved;
                return null;
            }

            next();

            return parameters;
        }

        function primary() {
            var token = peek();

            if (token.t === "num") {
                next();

                return [
                    "#",
                    parseFloat(token.v)
                ];
            }

            if (token.t === "str") {
                next();

                return [
                    "#",
                    token.v
                ];
            }

            if (token.t === "id") {
                next();

                if (token.v === "true") {
                    return ["#", true];
                }

                if (token.v === "false") {
                    return ["#", false];
                }

                if (token.v === "null") {
                    return ["#", null];
                }

                return ["$", token.v];
            }

            if (token.v === "(") {
                var parameters =
                    tryArrowParameters();

                if (parameters) {
                    return [
                        "=>",
                        parameters,
                        assignment()
                    ];
                }

                next();

                var grouped = assignment();

                eat(")");

                return grouped;
            }

            if (token.v === "{") {
                next();

                var pairs = [];

                while (peek().v !== "}") {
                    var key = next();

                    if (
                        key.t !== "id" &&
                        key.t !== "str"
                    ) {
                        throw new Error(
                            "kitwork: invalid object key"
                        );
                    }

                    eat(":");

                    pairs.push([
                        key.v,
                        assignment()
                    ]);

                    if (peek().v === ",") {
                        next();
                        continue;
                    }

                    break;
                }

                eat("}");

                return [
                    "{}",
                    pairs
                ];
            }

            if (token.v === "[") {
                next();

                var items = [];

                if (peek().v !== "]") {
                    for (; ;) {
                        items.push(assignment());

                        if (peek().v === ",") {
                            next();

                            if (peek().v === "]") {
                                throw new Error(
                                    "kitwork: trailing array comma"
                                );
                            }

                            continue;
                        }

                        break;
                    }
                }

                eat("]");

                return [
                    "[]",
                    items
                ];
            }

            throw new Error(
                "kitwork: unexpected " + token.v
            );
        }

        var node = assignment();

        if (peek().v === ";") {
            var sequence = [
                ";",
                node
            ];

            while (peek().v === ";") {
                next();

                if (peek().t === "eof") {
                    break;
                }

                sequence.push(assignment());
            }

            node =
                sequence.length === 2
                    ? node
                    : sequence;
        }

        if (peek().t !== "eof") {
            throw new Error(
                "kitwork: trailing tokens"
            );
        }

        return node;
    }

    function compile(source) {
        return parse(lex(source));
    }

    // ---------------------------------------------------------------------------
    // IR walker
    // ---------------------------------------------------------------------------

    function blockedKey(key) {
        return (
            key === "constructor" ||
            key === "__proto__" ||
            key === "prototype" ||
            key === "ownerDocument" ||
            key === "defaultView" ||
            key === "contentWindow" ||
            key === "window" ||
            key === "parent" ||
            key === "top" ||
            key === "self" ||
            key === "globalThis"
        );
    }

    function run(node, currentScope) {
        var operation = node[0];

        if (operation === "#") {
            return node[1];
        }

        if (operation === "$") {
            return currentScope[node[1]];
        }

        if (operation === "=") {
            if (blockedKey(node[1])) {
                return undefined;
            }

            var value =
                run(node[2], currentScope);

            currentScope[node[1]] = value;

            return value;
        }

        if (operation === "=$") {
            if (blockedKey(node[1])) {
                return undefined;
            }

            var rootValue =
                run(node[2], currentScope);

            currentScope.$[node[1]] =
                rootValue;

            return rootValue;
        }

        if (operation === "{}") {
            var object = {};

            for (
                var objectIndex = 0;
                objectIndex < node[1].length;
                objectIndex++
            ) {
                var pair =
                    node[1][objectIndex];

                if (blockedKey(pair[0])) {
                    continue;
                }

                object[pair[0]] =
                    run(pair[1], currentScope);
            }

            return object;
        }

        if (operation === "[]") {
            return node[1].map(
                function (item) {
                    return run(
                        item,
                        currentScope
                    );
                }
            );
        }

        if (operation === "=>") {
            return {
                __kitLambda: true,
                params: node[1],
                body: node[2]
            };
        }

        if (operation === ";") {
            var sequenceValue;

            for (
                var sequenceIndex = 1;
                sequenceIndex < node.length;
                sequenceIndex++
            ) {
                sequenceValue =
                    run(
                        node[sequenceIndex],
                        currentScope
                    );
            }

            return sequenceValue;
        }

        if (operation === "call") {
            var callable =
                run(node[1], currentScope);

            var callArguments =
                node[2].map(
                    function (argument) {
                        return run(
                            argument,
                            currentScope
                        );
                    }
                );

            if (callDepth >= 64) {
                throw new Error(
                    "kitwork: call depth exceeded"
                );
            }

            if (
                callable &&
                callable.__kitLambda
            ) {
                callDepth++;

                try {
                    if (!callable.params.length) {
                        return run(
                            callable.body,
                            currentScope
                        );
                    }

                    var local = {};

                    for (
                        var parameterIndex = 0;
                        parameterIndex <
                        callable.params.length;
                        parameterIndex++
                    ) {
                        local[
                            callable.params[
                            parameterIndex
                            ]
                        ] =
                            callArguments[
                            parameterIndex
                            ];
                    }

                    var overlay =
                        new Proxy(local, {
                            get: function (
                                target,
                                key
                            ) {
                                return key in target
                                    ? target[key]
                                    : currentScope[key];
                            },

                            set: function (
                                target,
                                key,
                                value
                            ) {
                                if (key in target) {
                                    target[key] = value;
                                } else {
                                    currentScope[key] =
                                        value;
                                }

                                return true;
                            }
                        });

                    return run(
                        callable.body,
                        overlay
                    );
                } finally {
                    callDepth--;
                }
            }

            if (
                typeof callable === "function"
            ) {
                callDepth++;

                try {
                    return callable.apply(
                        currentScope,
                        callArguments
                    );
                } finally {
                    callDepth--;
                }
            }

            return undefined;
        }

        if (operation === "?") {
            return run(
                node[1],
                currentScope
            )
                ? run(node[2], currentScope)
                : run(node[3], currentScope);
        }

        if (operation === ".") {
            var owner =
                run(node[1], currentScope);

            if (
                owner == null ||
                blockedKey(node[2])
            ) {
                return undefined;
            }

            return owner[node[2]];
        }

        if (operation === "()") {
            var methodOwner =
                run(node[1], currentScope);

            if (
                methodOwner == null ||
                blockedKey(node[2])
            ) {
                return undefined;
            }

            var method =
                methodOwner[node[2]];

            if (
                typeof method !== "function"
            ) {
                return undefined;
            }

            var methodArguments =
                node[3].map(
                    function (argument) {
                        return run(
                            argument,
                            currentScope
                        );
                    }
                );

            return method.apply(
                methodOwner,
                methodArguments
            );
        }

        if (operation === "u!") {
            return !run(
                node[1],
                currentScope
            );
        }

        if (operation === "u-") {
            return -run(
                node[1],
                currentScope
            );
        }

        var left =
            run(node[1], currentScope);

        var right =
            run(node[2], currentScope);

        switch (operation) {
            case "+":
                return left + right;
            case "-":
                return left - right;
            case "*":
                return left * right;
            case "/":
                return left / right;
            case "%":
                return left % right;
            case ">":
                return left > right;
            case "<":
                return left < right;
            case ">=":
                return left >= right;
            case "<=":
                return left <= right;
            case "==":
                return left == right;
            case "!=":
                return left != right;
            case "&&":
                return left && right;
            case "||":
                return left || right;
        }

        return undefined;
    }

    function execute(node, currentScope) {
        var result;

        try {
            result = run(
                node,
                currentScope
            );
        } catch (error) {
            return Promise.reject(error);
        }

        if (
            result &&
            typeof result.then === "function"
        ) {
            return result.then(
                function (value) {
                    scheduleRender();
                    return value;
                }
            );
        }

        scheduleRender();

        return Promise.resolve(result);
    }

    // ---------------------------------------------------------------------------
    // Directives
    // ---------------------------------------------------------------------------

    function selector(name) {
        return (
            "[data-kitwork-" +
            name +
            "],[data-kit-" +
            name +
            "]"
        );
    }

    function directive(
        element,
        name
    ) {
        var encoded =
            element.getAttribute(
                "data-kitwork-" + name
            );

        if (encoded) {
            if (!(encoded in directiveCache)) {
                try {
                    directiveCache[encoded] =
                        JSON.parse(encoded);
                } catch (_) {
                    directiveCache[encoded] =
                        null;
                }
            }

            return directiveCache[encoded];
        }

        var source =
            element.getAttribute(
                "data-kit-" + name
            );

        if (!source) {
            return null;
        }

        var key = "$" + source;

        if (!(key in directiveCache)) {
            try {
                directiveCache[key] =
                    compile(source);
            } catch (error) {
                directiveCache[key] =
                    null;

                if (
                    kitwork.runtime.development
                ) {
                    console.error(
                        "kitwork: expression compile failed",
                        source,
                        error
                    );
                }
            }
        }

        return directiveCache[key];
    }

    // ---------------------------------------------------------------------------
    // Element state and cleanup
    // ---------------------------------------------------------------------------

    function state(element) {
        if (!element[stateKey]) {
            element[stateKey] = {
                cleanups: []
            };
        }

        return element[stateKey];
    }

    function cleanupElement(element) {
        var current = element[stateKey];

        if (
            !current ||
            !current.cleanups
        ) {
            return;
        }

        for (
            var index = 0;
            index < current.cleanups.length;
            index++
        ) {
            try {
                current.cleanups[index]();
            } catch (_) { }
        }

        current.cleanups.length = 0;
    }

    function cleanupTree(node) {
        if (
            !node ||
            node.nodeType !== 1
        ) {
            return;
        }

        cleanupElement(node);

        node
            .querySelectorAll("*")
            .forEach(cleanupElement);
    }

    kitwork.onCleanup = function (
        element,
        callback
    ) {
        if (!element) {
            throw new TypeError(
                "kitwork.onCleanup: element is required"
            );
        }

        if (
            typeof callback !== "function"
        ) {
            throw new TypeError(
                "kitwork.onCleanup: callback must be a function"
            );
        }

        state(element)
            .cleanups
            .push(callback);

        return callback;
    };

    kitwork.cleanup = function (
        callback
    ) {
        if (
            typeof callback !== "function"
        ) {
            throw new TypeError(
                "kitwork.cleanup: callback must be a function"
            );
        }

        globalCleanups.add(callback);

        return function () {
            globalCleanups.delete(callback);
        };
    };

    // ---------------------------------------------------------------------------
    // Scopes and components
    // ---------------------------------------------------------------------------

    var scope =
        new Proxy(raw, {
            get: function (
                target,
                key
            ) {
                if (key === "$") {
                    return target;
                }

                if (key === "$app") {
                    return kitwork;
                }

                if (key in aliases) {
                    return aliases[key];
                }

                return key in target
                    ? target[key]
                    : 0;
            },

            set: function (
                target,
                key,
                value
            ) {
                target[key] = value;
                return true;
            }
        });

    function cloneState(value) {
        if (
            value === null ||
            typeof value !== "object"
        ) {
            return value;
        }

        try {
            return JSON.parse(
                JSON.stringify(value)
            );
        } catch (_) {
            return value;
        }
    }

    function seedComponent(
        target,
        definition
    ) {
        for (var key in definition) {
            if (
                !Object.prototype
                    .hasOwnProperty
                    .call(definition, key)
            ) {
                continue;
            }

            target[key] =
                typeof definition[key] ===
                    "function"
                    ? definition[key]
                    : cloneState(
                        definition[key]
                    );
        }
    }

    function componentTag(value) {
        var name = value;
        var alias = "";
        var version = "";
        var index;

        index = name.indexOf("=");

        if (index >= 0) {
            alias =
                name
                    .slice(index + 1)
                    .trim();

            name =
                name.slice(0, index);
        }

        index = name.indexOf("@");

        if (index >= 0) {
            version =
                name
                    .slice(index + 1)
                    .trim();

            name =
                name.slice(0, index);
        }

        return {
            name: name.trim(),
            alias: alias,
            version: version
        };
    }

    function boundaryScope(boundary) {
        var current = state(boundary);

        var componentValue =
            boundary.getAttribute(
                "data-kitwork-component"
            ) ||
            boundary.getAttribute(
                "data-kit-component"
            );

        if (componentValue) {
            var tag =
                componentTag(
                    componentValue
                );

            if (!current.scope) {
                current.scope = {};
            }

            if (tag.alias) {
                aliases[tag.alias] =
                    current.scope;
            }

            if (
                !current.seeded &&
                blueprints[tag.name]
            ) {
                seedComponent(
                    current.scope,
                    blueprints[tag.name]
                );

                current.seeded = true;

                runInit(boundary);
            }

            return current.scope;
        }

        if (current.scope) {
            return current.scope;
        }

        current.scope = {};

        var value =
            (
                boundary.getAttribute(
                    "data-kitwork-scope"
                ) ||
                boundary.getAttribute(
                    "data-kit-scope"
                ) ||
                ""
            ).trim();

        if (!value) {
            return current.scope;
        }

        try {
            var parent =
                boundary.parentElement
                    ? scopeFor(
                        boundary.parentElement
                    )
                    : scope;

            if (
                value.charAt(0) === "{"
            ) {
                var blueprint =
                    run(
                        compile(value),
                        parent
                    );

                if (
                    blueprint &&
                    typeof blueprint ===
                    "object"
                ) {
                    for (
                        var blueprintKey in blueprint
                    ) {
                        current.scope[
                            blueprintKey
                        ] =
                            blueprint[
                            blueprintKey
                            ];
                    }
                }

                runInit(boundary);
            } else if (
                value.indexOf("=") >= 0
            ) {
                var local =
                    current.scope;

                var initScope =
                    new Proxy(local, {
                        get: function (
                            target,
                            key
                        ) {
                            if (key === "$") {
                                return raw;
                            }

                            return key in target
                                ? target[key]
                                : parent[key];
                        },

                        set: function (
                            target,
                            key,
                            next
                        ) {
                            target[key] = next;
                            return true;
                        }
                    });

                run(
                    compile(value),
                    initScope
                );
            }
        } catch (error) {
            if (
                kitwork.runtime.development
            ) {
                console.error(
                    "kitwork: scope init failed",
                    error
                );
            }
        }

        return current.scope;
    }

    function runInit(boundary) {
        var current = state(boundary);

        if (current.inited) {
            return;
        }

        current.inited = true;

        var init =
            current.scope &&
            current.scope.init;

        if (!init) {
            return;
        }

        try {
            var currentScope =
                scopeFor(boundary);

            var result;

            if (
                typeof init === "function"
            ) {
                result =
                    init.apply(
                        currentScope,
                        []
                    );
            } else if (
                init.__kitLambda
            ) {
                result =
                    run(
                        init,
                        currentScope
                    );
            }

            if (
                result &&
                typeof result.then ===
                "function"
            ) {
                result
                    .then(scheduleRender)
                    .catch(function (error) {
                        currentScope.error =
                            error &&
                                error.message
                                ? error.message
                                : String(error);

                        scheduleRender();
                    });
            }
        } catch (error) {
            if (
                kitwork.runtime.development
            ) {
                console.error(
                    "kitwork: component init failed",
                    error
                );
            }
        }
    }

    function chainFor(element) {
        var chain = [];

        var boundary =
            element &&
                element.closest
                ? element.closest(SCOPE)
                : null;

        while (boundary) {
            chain.push(
                boundaryScope(boundary)
            );

            boundary =
                boundary.parentElement
                    ? boundary.parentElement
                        .closest(SCOPE)
                    : null;
        }

        chain.push(raw);

        return chain;
    }

    function scopeFor(element) {
        var boundary =
            element &&
                element.closest
                ? element.closest(SCOPE)
                : null;

        if (!boundary) {
            return scope;
        }

        var current =
            state(boundary);

        if (current.scopeProxy) {
            return current.scopeProxy;
        }

        current.scopeProxy =
            new Proxy(
                boundaryScope(boundary),
                {
                    get: function (
                        target,
                        key
                    ) {
                        if (key === "$") {
                            return raw;
                        }

                        if (key === "$app") {
                            return kitwork;
                        }

                        if (key in aliases) {
                            return aliases[key];
                        }

                        var chain =
                            chainFor(boundary);

                        for (
                            var index = 0;
                            index < chain.length;
                            index++
                        ) {
                            if (
                                key in chain[index]
                            ) {
                                return chain[index][key];
                            }
                        }

                        return 0;
                    },

                    set: function (
                        target,
                        key,
                        value
                    ) {
                        var chain =
                            chainFor(boundary);

                        for (
                            var index = 0;
                            index < chain.length;
                            index++
                        ) {
                            if (
                                key in chain[index]
                            ) {
                                chain[index][key] =
                                    value;

                                return true;
                            }
                        }

                        chain[0][key] =
                            value;

                        return true;
                    }
                }
            );

        return current.scopeProxy;
    }

    function elementScope(element) {
        var base =
            scopeFor(element);

        return new Proxy(base, {
            get: function (
                target,
                key
            ) {
                if (key === "$el") {
                    return element;
                }

                if (key === "$root") {
                    return (
                        (
                            element.closest &&
                            element.closest(SCOPE)
                        ) ||
                        document.documentElement
                    );
                }

                if (key === "$app") {
                    return kitwork;
                }

                if (key in aliases) {
                    return aliases[key];
                }

                return base[key];
            },

            set: function (
                target,
                key,
                value
            ) {
                base[key] = value;
                return true;
            }
        });
    }

    // ---------------------------------------------------------------------------
    // Models
    // ---------------------------------------------------------------------------

    function modelKey(element) {
        return (
            element.getAttribute(
                "data-kitwork-model"
            ) ||
            element.getAttribute(
                "data-kit-model"
            )
        );
    }

    function modelValue(element) {
        if (
            element.type === "checkbox"
        ) {
            return !!element.checked;
        }

        if (
            element.type === "number" ||
            element.type === "range"
        ) {
            var number =
                parseFloat(element.value);

            return Number.isNaN(number)
                ? 0
                : number;
        }

        return element.value || "";
    }

    function seedModels() {
        document
            .querySelectorAll(MODEL)
            .forEach(function (element) {
                var key =
                    modelKey(element);

                if (!key) {
                    return;
                }

                var chain =
                    chainFor(element);

                var found = false;

                for (
                    var index = 0;
                    index < chain.length;
                    index++
                ) {
                    if (
                        key in chain[index]
                    ) {
                        found = true;
                        break;
                    }
                }

                if (!found) {
                    chain[0][key] =
                        modelValue(element);
                }
            });
    }

    // ---------------------------------------------------------------------------
    // Rendering
    // ---------------------------------------------------------------------------

    function classNames(
        value,
        output
    ) {
        if (
            value == null ||
            value === false ||
            value === true
        ) {
            return output;
        }

        if (
            typeof value === "string"
        ) {
            value
                .split(/\s+/)
                .forEach(function (name) {
                    if (name) {
                        output.push(name);
                    }
                });

            return output;
        }

        if (Array.isArray(value)) {
            value.forEach(
                function (item) {
                    classNames(
                        item,
                        output
                    );
                }
            );

            return output;
        }

        if (
            typeof value === "object"
        ) {
            for (var key in value) {
                if (
                    Object.prototype
                        .hasOwnProperty
                        .call(value, key) &&
                    value[key]
                ) {
                    classNames(
                        key,
                        output
                    );
                }
            }
        }

        return output;
    }

    function render() {
        document
            .querySelectorAll(
                selector("text")
            )
            .forEach(function (element) {
                var expression =
                    directive(
                        element,
                        "text"
                    );

                if (!expression) {
                    return;
                }

                var value =
                    run(
                        expression,
                        scopeFor(element)
                    );

                element.textContent =
                    value == null
                        ? ""
                        : value;
            });

        document
            .querySelectorAll(
                selector("show")
            )
            .forEach(function (element) {
                var expression =
                    directive(
                        element,
                        "show"
                    );

                if (!expression) {
                    return;
                }

                element.hidden =
                    !run(
                        expression,
                        scopeFor(element)
                    );
            });

        document
            .querySelectorAll(
                selector("bind")
            )
            .forEach(function (element) {
                var expression =
                    directive(
                        element,
                        "bind"
                    );

                if (!expression) {
                    return;
                }

                var attributes =
                    run(
                        expression,
                        scopeFor(element)
                    );

                if (
                    !attributes ||
                    typeof attributes !==
                    "object"
                ) {
                    return;
                }

                for (
                    var name in attributes
                ) {
                    if (
                        !Object.prototype
                            .hasOwnProperty
                            .call(
                                attributes,
                                name
                            )
                    ) {
                        continue;
                    }

                    if (blockedKey(name)) {
                        continue;
                    }

                    var value =
                        attributes[name];

                    if (
                        value === false ||
                        value == null
                    ) {
                        element.removeAttribute(
                            name
                        );
                    } else if (
                        value === true
                    ) {
                        element.setAttribute(
                            name,
                            ""
                        );
                    } else if (
                        String(
                            element.getAttribute(
                                name
                            )
                        ) !== String(value)
                    ) {
                        element.setAttribute(
                            name,
                            value
                        );
                    }
                }
            });

        document
            .querySelectorAll(
                selector("class")
            )
            .forEach(function (element) {
                var expression =
                    directive(
                        element,
                        "class"
                    );

                if (!expression) {
                    return;
                }

                var wanted =
                    classNames(
                        run(
                            expression,
                            scopeFor(element)
                        ),
                        []
                    );

                var previous =
                    state(element)
                        .classes ||
                    [];

                previous.forEach(
                    function (name) {
                        if (
                            wanted.indexOf(name) <
                            0
                        ) {
                            element.classList
                                .remove(name);
                        }
                    }
                );

                wanted.forEach(
                    function (name) {
                        element.classList
                            .add(name);
                    }
                );

                state(element)
                    .classes =
                    wanted;
            });

        document
            .querySelectorAll(
                selector("validate")
            )
            .forEach(function (element) {
                var expression =
                    directive(
                        element,
                        "validate"
                    );

                if (!expression) {
                    return;
                }

                element.setAttribute(
                    "data-state",
                    run(
                        expression,
                        scopeFor(element)
                    )
                        ? "valid"
                        : "invalid"
                );
            });

        document
            .querySelectorAll(MODEL)
            .forEach(function (element) {
                var key =
                    modelKey(element);

                if (!key) {
                    return;
                }

                var currentScope =
                    scopeFor(element);

                if (
                    element.type ===
                    "checkbox"
                ) {
                    var checked =
                        !!currentScope[key];

                    if (
                        element.checked !==
                        checked
                    ) {
                        element.checked =
                            checked;
                    }

                    return;
                }

                var value =
                    currentScope[key] == null
                        ? ""
                        : String(
                            currentScope[key]
                        );

                if (
                    element.value !== value
                ) {
                    element.value = value;
                }
            });
    }

    function scheduleRender() {
        if (renderScheduled) {
            return;
        }

        renderScheduled = true;

        var queue =
            typeof queueMicrotask ===
                "function"
                ? queueMicrotask
                : function (callback) {
                    setTimeout(
                        callback,
                        0
                    );
                };

        queue(function () {
            renderScheduled = false;
            render();
        });
    }

    // ---------------------------------------------------------------------------
    // Public state API
    // ---------------------------------------------------------------------------

    kitwork.get = function (key) {
        return scope[key];
    };

    kitwork.set = function (
        key,
        value
    ) {
        scope[key] = value;
        scheduleRender();

        return value;
    };

    kitwork.update = function (
        values
    ) {
        if (
            !values ||
            typeof values !== "object"
        ) {
            return kitwork;
        }

        Object.keys(values)
            .forEach(function (key) {
                scope[key] =
                    values[key];
            });

        scheduleRender();

        return kitwork;
    };

    kitwork.render =
        scheduleRender;

    kitwork.compile = compile;

    kitwork.run = function (
        sourceOrIR,
        values
    ) {
        var currentScope =
            values || scope;

        var node =
            typeof sourceOrIR ===
                "string"
                ? compile(sourceOrIR)
                : sourceOrIR;

        return execute(
            node,
            currentScope
        );
    };

    // ---------------------------------------------------------------------------
    // Components and actions
    // ---------------------------------------------------------------------------

    kitwork.component = function (
        name,
        definition
    ) {
        if (
            typeof name !== "string" ||
            name.trim() === ""
        ) {
            throw new TypeError(
                "kitwork.component: name is required"
            );
        }

        if (
            !definition ||
            typeof definition !==
            "object"
        ) {
            throw new TypeError(
                "kitwork.component: definition must be an object"
            );
        }

        blueprints[
            name.trim()
        ] =
            definition;

        scheduleRender();

        return kitwork;
    };

    kitwork.action = function (
        name,
        callback
    ) {
        if (
            typeof name !== "string" ||
            name.trim() === ""
        ) {
            throw new TypeError(
                "kitwork.action: name is required"
            );
        }

        if (
            typeof callback !==
            "function"
        ) {
            throw new TypeError(
                "kitwork.action: callback must be a function"
            );
        }

        behaviors[
            name.trim()
        ] =
            callback;

        return kitwork;
    };

    kitwork.behavior =
        kitwork.action;

    kitwork.alias = function (
        name,
        value
    ) {
        if (
            typeof name !== "string" ||
            name.trim() === ""
        ) {
            throw new TypeError(
                "kitwork.alias: name is required"
            );
        }

        aliases[
            name.trim()
        ] =
            value;

        return kitwork;
    };

    kitwork.state = state;

    // ---------------------------------------------------------------------------
    // Remember
    // ---------------------------------------------------------------------------

    var remembered =
        Object.create(null);

    function storageKey(key) {
        return "kitwork:" + key;
    }

    function storageGet(key) {
        try {
            return localStorage.getItem(
                storageKey(key)
            );
        } catch (_) {
            return null;
        }
    }

    function storageSet(
        key,
        value
    ) {
        try {
            localStorage.setItem(
                storageKey(key),
                value
            );

            return true;
        } catch (_) {
            return false;
        }
    }

    function rememberKey(key) {
        if (
            !key ||
            remembered[key]
        ) {
            return;
        }

        remembered[key] = true;

        var saved =
            storageGet(key);

        var initial =
            raw[key];

        Object.defineProperty(
            raw,
            key,
            {
                get: function () {
                    var value =
                        storageGet(key);

                    if (value === null) {
                        return undefined;
                    }

                    try {
                        return JSON.parse(value);
                    } catch (_) {
                        return value;
                    }
                },

                set: function (value) {
                    storageSet(
                        key,
                        JSON.stringify(value)
                    );
                },

                configurable: true,
                enumerable: true
            }
        );

        if (
            saved === null &&
            initial !== undefined
        ) {
            raw[key] = initial;
        }
    }

    function parseKeys(value) {
        return (
            value || ""
        )
            .trim()
            .replace(/^\[/, "")
            .replace(/\]$/, "")
            .split(/[\s,]+/)
            .filter(Boolean);
    }

    function loadRemembered() {
        document
            .querySelectorAll(
                REMEMBER
            )
            .forEach(function (element) {
                parseKeys(
                    element.getAttribute(
                        "data-kitwork-remember"
                    ) ||
                    element.getAttribute(
                        "data-kit-remember"
                    )
                ).forEach(rememberKey);
            });
    }

    kitwork.remember = function () {
        for (
            var index = 0;
            index < arguments.length;
            index++
        ) {
            rememberKey(
                arguments[index]
            );
        }

        scheduleRender();

        return kitwork;
    };

    // ---------------------------------------------------------------------------
    // Live SSE
    // ---------------------------------------------------------------------------

    function liveTarget(element) {
        var boundary =
            element.closest
                ? element.closest(SCOPE)
                : null;

        return boundary
            ? boundaryScope(boundary)
            : raw;
    }

    function syncLive() {
        if (!window.EventSource) {
            return;
        }

        var wanted =
            Object.create(null);

        document
            .querySelectorAll(LIVE)
            .forEach(function (element) {
                var url =
                    element.getAttribute(
                        "data-kitwork-live"
                    ) ||
                    element.getAttribute(
                        "data-kit-live"
                    );

                if (!url) {
                    return;
                }

                if (!wanted[url]) {
                    wanted[url] = [];
                }

                wanted[url].push(element);
            });

        Object.keys(wanted)
            .forEach(function (url) {
                if (streams[url]) {
                    streams[url].elements =
                        wanted[url];

                    return;
                }

                var record = {
                    source: new EventSource(url),
                    elements: wanted[url]
                };

                record.source.onmessage =
                    function (event) {
                        var patch;

                        try {
                            patch =
                                JSON.parse(
                                    event.data
                                );
                        } catch (_) {
                            return;
                        }

                        if (
                            !patch ||
                            typeof patch !==
                            "object" ||
                            Array.isArray(patch)
                        ) {
                            return;
                        }

                        record.elements
                            .forEach(
                                function (element) {
                                    var target =
                                        liveTarget(
                                            element
                                        );

                                    Object.keys(patch)
                                        .forEach(
                                            function (key) {
                                                target[key] =
                                                    patch[key];
                                            }
                                        );
                                }
                            );

                        scheduleRender();
                    };

                streams[url] =
                    record;
            });

        Object.keys(streams)
            .forEach(function (url) {
                if (!wanted[url]) {
                    streams[url]
                        .source
                        .close();

                    delete streams[url];
                }
            });
    }

    // ---------------------------------------------------------------------------
    // API data source
    // ---------------------------------------------------------------------------

    function syncApi() {
        if (!window.fetch) {
            return;
        }

        document
            .querySelectorAll(API)
            .forEach(function (element) {
                var current =
                    state(element);

                if (current.apiState) {
                    return;
                }

                var url =
                    element.getAttribute(
                        "data-kitwork-api"
                    ) ||
                    element.getAttribute(
                        "data-kit-api"
                    );

                if (!url) {
                    return;
                }

                current.apiState =
                    "loading";

                element.setAttribute(
                    "data-state",
                    "loading"
                );

                var controller =
                    typeof AbortController ===
                        "function"
                        ? new AbortController()
                        : null;

                if (controller) {
                    kitwork.onCleanup(
                        element,
                        function () {
                            controller.abort();
                        }
                    );
                }

                fetch(url, {
                    credentials:
                        "same-origin",

                    headers: {
                        Accept:
                            "application/json"
                    },

                    signal:
                        controller
                            ? controller.signal
                            : undefined
                })
                    .then(function (response) {
                        if (!response.ok) {
                            throw new Error(
                                "HTTP " +
                                response.status
                            );
                        }

                        return response.json();
                    })
                    .then(function (data) {
                        var target =
                            boundaryScope(element);

                        if (
                            data &&
                            typeof data ===
                            "object" &&
                            !Array.isArray(data)
                        ) {
                            Object.keys(data)
                                .forEach(
                                    function (key) {
                                        target[key] =
                                            data[key];
                                    }
                                );
                        } else {
                            target.data = data;
                        }

                        current.apiState =
                            "ready";

                        element.setAttribute(
                            "data-state",
                            "ready"
                        );

                        scheduleRender();
                    })
                    .catch(function (error) {
                        if (
                            error &&
                            error.name ===
                            "AbortError"
                        ) {
                            return;
                        }

                        current.apiState =
                            "error";

                        element.setAttribute(
                            "data-state",
                            "error"
                        );

                        boundaryScope(element)
                            .error =
                            error &&
                                error.message
                                ? error.message
                                : String(error);

                        scheduleRender();
                    });
            });
    }

    // ---------------------------------------------------------------------------
    // Visible trigger
    // ---------------------------------------------------------------------------

    function bindVisible() {
        if (
            !(
                "IntersectionObserver" in
                window
            )
        ) {
            return;
        }

        document
            .querySelectorAll(VISIBLE)
            .forEach(function (element) {
                var current =
                    state(element);

                if (
                    current.visibilityObserver
                ) {
                    return;
                }

                var observer =
                    new IntersectionObserver(
                        function (entries) {
                            if (
                                entries[0] &&
                                entries[0].isIntersecting
                            ) {
                                fireAction(
                                    element,
                                    null
                                );
                            }
                        },
                        {
                            rootMargin:
                                "300px"
                        }
                    );

                current.visibilityObserver =
                    observer;

                observer.observe(element);

                kitwork.onCleanup(
                    element,
                    function () {
                        observer.disconnect();
                    }
                );
            });
    }

    // ---------------------------------------------------------------------------
    // Morph
    // ---------------------------------------------------------------------------

    function morph(
        fromNode,
        toNode
    ) {
        if (
            fromNode.nodeType !==
            toNode.nodeType
        ) {
            fromNode.replaceWith(
                toNode.cloneNode(true)
            );

            return;
        }

        if (
            fromNode.nodeType === 3
        ) {
            if (
                fromNode.nodeValue !==
                toNode.nodeValue
            ) {
                fromNode.nodeValue =
                    toNode.nodeValue;
            }

            return;
        }

        if (
            fromNode.nodeType !== 1
        ) {
            return;
        }

        if (
            fromNode.tagName !==
            toNode.tagName
        ) {
            fromNode.replaceWith(
                toNode.cloneNode(true)
            );

            return;
        }

        var fromAttributes =
            Array.prototype.slice.call(
                fromNode.attributes
            );

        var toAttributes =
            Array.prototype.slice.call(
                toNode.attributes
            );

        fromAttributes.forEach(
            function (attribute) {
                if (
                    !toNode.hasAttribute(
                        attribute.name
                    )
                ) {
                    fromNode.removeAttribute(
                        attribute.name
                    );
                }
            }
        );

        toAttributes.forEach(
            function (attribute) {
                if (
                    fromNode.getAttribute(
                        attribute.name
                    ) !== attribute.value
                ) {
                    fromNode.setAttribute(
                        attribute.name,
                        attribute.value
                    );
                }
            }
        );

        if (
            fromNode.tagName ===
            "INPUT" ||
            fromNode.tagName ===
            "TEXTAREA"
        ) {
            if (
                fromNode.value !==
                toNode.value
            ) {
                fromNode.value =
                    toNode.value;
            }

            if (
                fromNode.tagName ===
                "INPUT"
            ) {
                fromNode.checked =
                    toNode.checked;
            }
        } else if (
            fromNode.tagName ===
            "SELECT"
        ) {
            fromNode.value =
                toNode.value;
        }

        function keyOf(node) {
            if (
                node.nodeType !== 1
            ) {
                return null;
            }

            return (
                node.getAttribute(
                    "data-kitwork-key"
                ) ||
                node.getAttribute(
                    "data-kit-key"
                ) ||
                node.getAttribute(
                    "data-key"
                )
            );
        }

        var fromChildren =
            Array.prototype.slice.call(
                fromNode.childNodes
            );

        var toChildren =
            Array.prototype.slice.call(
                toNode.childNodes
            );

        var keyed =
            Object.create(null);

        fromChildren.forEach(
            function (child) {
                var key =
                    keyOf(child);

                if (key) {
                    keyed[key] = child;
                }
            }
        );

        var cursor = 0;

        toChildren.forEach(
            function (desired) {
                var desiredKey =
                    keyOf(desired);

                var current = null;

                if (
                    desiredKey &&
                    keyed[desiredKey]
                ) {
                    current =
                        keyed[desiredKey];

                    var keyedIndex =
                        fromChildren.indexOf(
                            current
                        );

                    if (keyedIndex >= 0) {
                        fromChildren.splice(
                            keyedIndex,
                            1
                        );
                    }
                } else {
                    for (
                        var index = 0;
                        index <
                        fromChildren.length;
                        index++
                    ) {
                        var candidate =
                            fromChildren[index];

                        if (
                            keyOf(candidate)
                        ) {
                            continue;
                        }

                        if (
                            candidate.nodeType ===
                            desired.nodeType &&
                            (
                                candidate.nodeType !==
                                1 ||
                                candidate.tagName ===
                                desired.tagName
                            )
                        ) {
                            current = candidate;

                            fromChildren.splice(
                                index,
                                1
                            );

                            break;
                        }
                    }
                }

                var reference =
                    fromNode.childNodes[
                    cursor
                    ] ||
                    null;

                if (current) {
                    if (
                        current !== reference
                    ) {
                        fromNode.insertBefore(
                            current,
                            reference
                        );
                    }

                    morph(
                        current,
                        desired
                    );
                } else {
                    fromNode.insertBefore(
                        desired.cloneNode(true),
                        reference
                    );
                }

                cursor++;
            }
        );

        while (
            fromNode.childNodes.length >
            cursor
        ) {
            var leftover =
                fromNode.childNodes[
                fromNode.childNodes.length -
                1
                ];

            cleanupTree(leftover);
            leftover.remove();
        }
    }

    kitwork.morph = morph;

    // ---------------------------------------------------------------------------
    // Delegated events
    // ---------------------------------------------------------------------------

    function target(element) {
        var selectorValue =
            element.getAttribute(
                "data-kitwork-target"
            ) ||
            element.getAttribute(
                "data-kit-target"
            );

        return selectorValue
            ? document.querySelector(
                selectorValue
            )
            : element;
    }

    function fireAction(
        element,
        event
    ) {
        var name =
            element.getAttribute(
                "data-kitwork-action"
            ) ||
            element.getAttribute(
                "data-kit-action"
            );

        var callback =
            behaviors[name];

        if (!callback) {
            return undefined;
        }

        var result =
            callback(
                element,
                event,
                target(element)
            );

        if (
            result &&
            typeof result.then ===
            "function"
        ) {
            return result
                .then(scheduleRender)
                .catch(function (error) {
                    scopeFor(element).error =
                        error &&
                            error.message
                            ? error.message
                            : String(error);

                    scheduleRender();

                    throw error;
                });
        }

        scheduleRender();

        return result;
    }

    document.addEventListener(
        "click",
        function (event) {
            var expressionElement =
                event.target.closest &&
                event.target.closest(
                    selector("click")
                );

            if (expressionElement) {
                var expression =
                    directive(
                        expressionElement,
                        "click"
                    );

                if (expression) {
                    execute(
                        expression,
                        elementScope(
                            expressionElement
                        )
                    ).catch(
                        function (error) {
                            scopeFor(
                                expressionElement
                            ).error =
                                error &&
                                    error.message
                                    ? error.message
                                    : String(error);

                            scheduleRender();
                        }
                    );
                }
            }

            var actionElement =
                event.target.closest &&
                event.target.closest(
                    ACTION
                );

            if (actionElement) {
                fireAction(
                    actionElement,
                    event
                );
            }
        }
    );

    document.addEventListener(
        "input",
        function (event) {
            var element =
                event.target.closest &&
                event.target.closest(
                    MODEL
                );

            if (!element) {
                return;
            }

            scopeFor(element)[
                modelKey(element)
            ] =
                modelValue(element);

            scheduleRender();
        }
    );

    document.addEventListener(
        "change",
        function (event) {
            var element =
                event.target.closest &&
                event.target.closest(
                    MODEL
                );

            if (
                !element ||
                element.type !==
                "checkbox"
            ) {
                return;
            }

            scopeFor(element)[
                modelKey(element)
            ] =
                modelValue(element);

            scheduleRender();
        }
    );

    document.addEventListener(
        "submit",
        function (event) {
            var form =
                event.target;

            if (
                form.matches &&
                (
                    form.matches(
                        '[data-state="invalid"]'
                    ) ||
                    form.querySelector(
                        '[data-state="invalid"]'
                    )
                )
            ) {
                event.preventDefault();
                return;
            }

            if (
                form.getAttribute &&
                (
                    form.getAttribute(
                        "data-kitwork-action"
                    ) ||
                    form.getAttribute(
                        "data-kit-action"
                    )
                )
            ) {
                fireAction(
                    form,
                    event
                );
            }
        },
        true
    );

    // ---------------------------------------------------------------------------
    // Observer and synchronization
    // ---------------------------------------------------------------------------

    function scheduleSync() {
        if (syncScheduled) {
            return;
        }

        syncScheduled = true;

        setTimeout(function () {
            syncScheduled = false;

            loadRemembered();
            seedModels();
            syncApi();
            syncLive();
            bindVisible();
            scheduleRender();
        }, 0);
    }

    var observer =
        new MutationObserver(
            function (records) {
                records.forEach(
                    function (record) {
                        record.removedNodes
                            .forEach(cleanupTree);
                    }
                );

                scheduleSync();
            }
        );

    observer.observe(
        document.documentElement,
        {
            childList: true,
            subtree: true
        }
    );

    kitwork.cleanup(function () {
        observer.disconnect();
    });

    window.addEventListener(
        "storage",
        function (event) {
            if (
                event.key &&
                event.key.indexOf(
                    "kitwork:"
                ) === 0
            ) {
                scheduleRender();
            }
        }
    );

    // ---------------------------------------------------------------------------
    // Boot and destroy
    // ---------------------------------------------------------------------------

    function boot() {
        loadRemembered();
        seedModels();
        syncApi();
        syncLive();
        bindVisible();
        scheduleRender();

        document.dispatchEvent(
            new CustomEvent(
                "kitwork:ready",
                {
                    detail: {
                        runtime:
                            kitwork.runtime.info()
                    }
                }
            )
        );
    }

    kitwork.destroy = function () {
        Object.keys(streams)
            .forEach(function (url) {
                streams[url]
                    .source
                    .close();

                delete streams[url];
            });

        globalCleanups
            .forEach(function (cleanup) {
                try {
                    cleanup();
                } catch (_) { }
            });

        globalCleanups.clear();

        document
            .querySelectorAll("*")
            .forEach(cleanupElement);

        kitwork.runtime.booted =
            false;
    };

    if (
        document.readyState ===
        "loading"
    ) {
        document.addEventListener(
            "DOMContentLoaded",
            boot,
            {
                once: true
            }
        );
    } else {
        boot();
    }

    document.addEventListener(
        "kitwork:load",
        scheduleSync
    );
})(window, document);

