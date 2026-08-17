PRAGMA foreign_keys=OFF;
BEGIN TRANSACTION;
CREATE TABLE nodes (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    name TEXT NOT NULL,
    qualified_name TEXT NOT NULL,
    file_path TEXT NOT NULL,
    language TEXT NOT NULL,
    start_line INTEGER NOT NULL,
    end_line INTEGER NOT NULL,
    start_column INTEGER NOT NULL,
    end_column INTEGER NOT NULL,
    docstring TEXT,
    signature TEXT,
    visibility TEXT,
    is_exported INTEGER DEFAULT 0,
    is_async INTEGER DEFAULT 0,
    is_static INTEGER DEFAULT 0,
    is_abstract INTEGER DEFAULT 0,
    decorators TEXT, -- JSON array
    type_parameters TEXT, -- JSON array
    return_type TEXT, -- normalized return/result type name (e.g. C++ method return, for receiver-type inference)
    updated_at INTEGER NOT NULL
);
INSERT INTO nodes VALUES('file:calc/calc.go','file','calc.go','calc/calc.go','calc/calc.go','go',1,19,0,0,NULL,NULL,NULL,0,0,0,0,NULL,NULL,NULL,1786987361605);
INSERT INTO nodes VALUES('struct:17e6085677ebc64b4af75398392b8b4e','struct','Adder','Adder','calc/calc.go','go',5,7,5,1,NULL,NULL,NULL,1,0,0,0,NULL,NULL,NULL,1786987361605);
INSERT INTO nodes VALUES('method:eb6726d49deacd747e04bb29752d195d','method','Add','Adder::Add','calc/calc.go','go',10,14,0,1,'Add folds n into the running total.','(n int) int',NULL,0,0,0,0,NULL,NULL,'int',1786987361605);
INSERT INTO nodes VALUES('function:31bc4c279867d3e74a9ae9c86aec65f8','function','scale','scale','calc/calc.go','go',16,18,0,1,NULL,'(n int) int',NULL,0,0,0,0,NULL,NULL,'int',1786987361605);
INSERT INTO nodes VALUES('file:calc/run.go','file','run.go','calc/run.go','calc/run.go','go',1,15,0,0,NULL,NULL,NULL,0,0,0,0,NULL,NULL,NULL,1786987361605);
INSERT INTO nodes VALUES('constant:4e826dcb9082c66e42103c7c29063ffd','constant','Factor','Factor','calc/run.go','go',4,4,6,16,'Factor scales every Add.','= 2',NULL,0,0,0,0,NULL,NULL,NULL,1786987361605);
INSERT INTO nodes VALUES('function:ace415ac6c02b3e2711df664545c8160','function','Run','Run','calc/run.go','go',7,14,0,1,'Run adds every value in ns and reports the total.','(ns []int) int',NULL,1,0,0,0,NULL,NULL,'int',1786987361605);
COMMIT;
PRAGMA foreign_keys=OFF;
BEGIN TRANSACTION;
CREATE TABLE edges (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source TEXT NOT NULL,
    target TEXT NOT NULL,
    kind TEXT NOT NULL,
    metadata TEXT, -- JSON object
    line INTEGER,
    col INTEGER,
    provenance TEXT DEFAULT NULL,
    FOREIGN KEY (source) REFERENCES nodes(id) ON DELETE CASCADE,
    FOREIGN KEY (target) REFERENCES nodes(id) ON DELETE CASCADE
);
INSERT INTO edges VALUES(1,'file:calc/calc.go','struct:17e6085677ebc64b4af75398392b8b4e','contains',NULL,NULL,NULL,NULL);
INSERT INTO edges VALUES(2,'file:calc/calc.go','method:eb6726d49deacd747e04bb29752d195d','contains',NULL,NULL,NULL,NULL);
INSERT INTO edges VALUES(3,'struct:17e6085677ebc64b4af75398392b8b4e','method:eb6726d49deacd747e04bb29752d195d','contains',NULL,NULL,NULL,NULL);
INSERT INTO edges VALUES(4,'file:calc/calc.go','function:31bc4c279867d3e74a9ae9c86aec65f8','contains',NULL,NULL,NULL,NULL);
INSERT INTO edges VALUES(5,'file:calc/run.go','constant:4e826dcb9082c66e42103c7c29063ffd','contains',NULL,NULL,NULL,NULL);
INSERT INTO edges VALUES(6,'file:calc/run.go','function:ace415ac6c02b3e2711df664545c8160','contains',NULL,NULL,NULL,NULL);
INSERT INTO edges VALUES(7,'function:ace415ac6c02b3e2711df664545c8160','constant:4e826dcb9082c66e42103c7c29063ffd','references','{"valueRef":true}',NULL,NULL,NULL);
INSERT INTO edges VALUES(8,'method:eb6726d49deacd747e04bb29752d195d','function:31bc4c279867d3e74a9ae9c86aec65f8','calls','{"confidence":0.9,"resolvedBy":"exact-match","refName":"scale"}',11,11,NULL);
INSERT INTO edges VALUES(9,'function:ace415ac6c02b3e2711df664545c8160','struct:17e6085677ebc64b4af75398392b8b4e','instantiates','{"confidence":0.9,"resolvedBy":"exact-match","refName":"Adder"}',8,7,NULL);
INSERT INTO edges VALUES(10,'function:ace415ac6c02b3e2711df664545c8160','method:eb6726d49deacd747e04bb29752d195d','calls','{"confidence":0.9,"resolvedBy":"instance-method","refName":"a.Add"}',10,2,NULL);
COMMIT;
